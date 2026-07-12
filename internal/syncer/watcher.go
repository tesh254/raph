package syncer

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// mu guards watched, which is mutated from two goroutines: the worker's
	// ensureDirs reconcile and the event loop's create/remove handling. Without
	// it a concurrent map read+write can trigger a fatal runtime error that
	// takes down the whole sync daemon.
	mu      sync.Mutex
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
			w.addWatch(path)
			return nil
		})
	}
}

// addWatch registers a directory watch if not already present. Safe to call
// from either goroutine.
func (w *repoWatcher) addWatch(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[path]; ok {
		return
	}
	if err := w.fsw.Add(path); err != nil {
		verbose.Printf("watch add failed dir=%s: %v", path, err)
		return
	}
	w.watched[path] = struct{}{}
}

// removeWatch drops a directory and any of its descendants from the watch set
// (fsnotify already tears down the OS-level watch when a dir is deleted, but the
// map entry would otherwise linger and block re-watching if the path is later
// recreated — e.g. across a branch switch or a regenerated build dir).
func (w *repoWatcher) removeWatch(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	prefix := path + string(filepath.Separator)
	for p := range w.watched {
		if p == path || strings.HasPrefix(p, prefix) {
			delete(w.watched, p)
			_ = w.fsw.Remove(p)
		}
	}
}

func (w *repoWatcher) loop(ctx context.Context) {
	// A panic in this goroutine would otherwise crash the whole daemon with no
	// trace; log and exit the loop instead.
	defer func() {
		if r := recover(); r != nil {
			verbose.Printf("watcher loop panic: %v", r)
		}
	}()
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
			// Prune watches for removed/renamed directories before filtering, so a
			// recreated path can be watched again.
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				w.removeWatch(event.Name)
			}
			if !relevantEvent(event) {
				continue
			}
			// Newly created directories must be watched immediately so events
			// inside them are not missed before the next reconcile.
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() && !indexer.ShouldSkipDir(filepath.Base(event.Name)) {
					w.addWatch(event.Name)
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
	// A newly-created directory must pass so its subtree gets watched. A newly
	// created file, though, should only wake a sync if it's indexable — otherwise
	// lockfiles, temp files, logs, and images trigger an expensive full scan.
	if event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			return true
		}
		return indexer.IndexablePath(event.Name)
	}
	return indexer.IndexablePath(event.Name)
}
