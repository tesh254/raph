package syncer

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"raph/internal/indexer"
	"raph/internal/verbose"

	"github.com/fsnotify/fsnotify"
)

// debounceWindow is how long the watcher coalesces a burst of filesystem events
// before signalling a sync. Kept small so an agent sees an updated graph within
// a few hundred milliseconds of a save, without thrashing on rapid writes.
const debounceWindow = 150 * time.Millisecond

// repoWatcher wraps fsnotify to deliver debounced change signals for the set of
// registered repositories. fsnotify is non-recursive, so directories are added
// individually and refreshed as the tree changes.
type repoWatcher struct {
	fsw     *fsnotify.Watcher
	trigger chan struct{}
	watched map[string]struct{}
}

func newRepoWatcher(ctx context.Context, trigger chan struct{}) (*repoWatcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &repoWatcher{fsw: fsw, trigger: trigger, watched: map[string]struct{}{}}
	go w.loop(ctx)
	return w, nil
}

func (w *repoWatcher) Close() error {
	if w == nil || w.fsw == nil {
		return nil
	}
	return w.fsw.Close()
}

// ensureDirs walks every repository root and watches each indexable directory,
// adding watches for directories created since the last reconcile.
func (w *repoWatcher) ensureDirs(roots []string) {
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if path != root && indexer.ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			if _, ok := w.watched[path]; ok {
				return nil
			}
			if addErr := w.fsw.Add(path); addErr != nil {
				verbose.Printf("watch add failed dir=%s: %v", path, addErr)
				return nil
			}
			w.watched[path] = struct{}{}
			return nil
		})
	}
}

func (w *repoWatcher) loop(ctx context.Context) {
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if !relevantEvent(event) {
				continue
			}
			// Newly created directories must be watched immediately so events
			// inside them are not missed before the next reconcile.
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() && !indexer.ShouldSkipDir(filepath.Base(event.Name)) {
					if _, ok := w.watched[event.Name]; !ok {
						if err := w.fsw.Add(event.Name); err == nil {
							w.watched[event.Name] = struct{}{}
						}
					}
				}
			}
			if timer == nil {
				timer = time.NewTimer(debounceWindow)
			} else {
				timer.Reset(debounceWindow)
			}
			timerC = timer.C
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
		case <-timerC:
			timerC = nil
			select {
			case w.trigger <- struct{}{}:
			default:
			}
		}
	}
}

// relevantEvent filters out chmod-only noise and events inside skipped
// directories. Directory creates pass through so new trees get watched.
func relevantEvent(event fsnotify.Event) bool {
	if event.Op == fsnotify.Chmod {
		return false
	}
	base := filepath.Base(event.Name)
	if indexer.ShouldSkipDir(base) {
		return false
	}
	// A create may be a directory (no extension); let those through. For files,
	// only react to indexable extensions to avoid waking on lockfiles etc.
	if event.Op&fsnotify.Create != 0 {
		return true
	}
	return indexer.IndexablePath(event.Name)
}
