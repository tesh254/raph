package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/indexer"
)

type Repository struct {
	Path         string                       `json:"path"`
	NoEmbeddings bool                         `json:"no_embeddings,omitempty"`
	Files        map[string]indexer.FileState `json:"files,omitempty"`
}

type Registry struct {
	Repositories []Repository `json:"repositories"`
}

type Paths struct {
	Registry string
	PID      string
	Log      string
}

func RuntimePaths() (Paths, error) {
	cfgPaths, err := config.EnsureBaseLayout()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Registry: filepath.Join(cfgPaths.DataDir, "sync.json"),
		PID:      filepath.Join(cfgPaths.DataDir, "sync.pid"),
		Log:      filepath.Join(cfgPaths.DataDir, "sync.log"),
	}, nil
}

func Register(path string, noEmbeddings bool) (Repository, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Repository{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return Repository{}, err
	}
	if !info.IsDir() {
		return Repository{}, fmt.Errorf("%s is not a directory", absPath)
	}

	registry, paths, err := load()
	if err != nil {
		return Repository{}, err
	}
	repo := Repository{Path: absPath, NoEmbeddings: noEmbeddings}
	repo.Files, err = indexer.CollectFileStates(absPath)
	if err != nil {
		return Repository{}, err
	}
	found := false
	for idx := range registry.Repositories {
		if samePath(registry.Repositories[idx].Path, absPath) {
			registry.Repositories[idx] = repo
			found = true
			break
		}
	}
	if !found {
		registry.Repositories = append(registry.Repositories, repo)
	}
	if err := save(paths.Registry, registry); err != nil {
		return Repository{}, err
	}
	return repo, nil
}

func Remove(ctx context.Context, path string, clean bool) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	registry, paths, err := load()
	if err != nil {
		return err
	}
	next := registry.Repositories[:0]
	removed := false
	for _, repo := range registry.Repositories {
		if samePath(repo.Path, absPath) {
			removed = true
			continue
		}
		next = append(next, repo)
	}
	if !removed {
		return fmt.Errorf("%s is not registered for sync", absPath)
	}
	registry.Repositories = next
	if err := save(paths.Registry, registry); err != nil {
		return err
	}
	if clean {
		store, err := db.InitStorage()
		if err != nil {
			return err
		}
		defer store.Close()
		idx, err := indexer.New(store, nil, absPath, true)
		if err != nil {
			return err
		}
		return store.DeleteWorkspace(ctx, idx.WorkspaceID())
	}
	return nil
}

func List() ([]Repository, error) {
	registry, _, err := load()
	return registry.Repositories, err
}

func Start(interval time.Duration) (bool, error) {
	running, _, err := Status()
	if err != nil {
		return false, err
	}
	if running {
		return false, nil
	}
	paths, err := RuntimePaths()
	if err != nil {
		return false, err
	}
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	cmd := exec.Command(executable, "sync", "--worker", "--interval", interval.String())
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return false, err
	}
	_ = logFile.Close()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		running, _, statusErr := Status()
		if statusErr != nil {
			return false, statusErr
		}
		if running {
			return true, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false, fmt.Errorf("sync worker did not start; inspect %s", paths.Log)
}

func Status() (bool, int, error) {
	paths, err := RuntimePaths()
	if err != nil {
		return false, 0, err
	}
	data, err := os.ReadFile(paths.PID)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		_ = os.Remove(paths.PID)
		return false, 0, nil
	}
	if !processAlive(pid) {
		_ = os.Remove(paths.PID)
		return false, 0, nil
	}
	return true, pid, nil
}

func Stop() (bool, error) {
	running, pid, err := Status()
	if err != nil || !running {
		return false, err
	}
	if err := stopProcess(pid); err != nil {
		return false, err
	}
	paths, err := RuntimePaths()
	if err == nil {
		_ = os.Remove(paths.PID)
	}
	return true, nil
}

func RunWorker(ctx context.Context, interval time.Duration) error {
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	paths, err := RuntimePaths()
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(paths.PID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("sync worker is already running")
		}
		return err
	}
	if _, err := fmt.Fprint(lock, os.Getpid()); err != nil {
		_ = lock.Close()
		_ = os.Remove(paths.PID)
		return err
	}
	_ = lock.Close()
	defer os.Remove(paths.PID)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	snapshots := make(map[string]map[string]indexer.FileState)
	for {
		if err := syncOnce(ctx, snapshots); err != nil {
			fmt.Fprintf(os.Stderr, "%s sync error: %v\n", time.Now().Format(time.RFC3339), err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func syncOnce(ctx context.Context, snapshots map[string]map[string]indexer.FileState) error {
	repositories, err := List()
	if err != nil {
		return err
	}
	cfg, err := config.LoadConfigIfPresent()
	if err != nil {
		return err
	}
	for _, repo := range repositories {
		current, err := indexer.CollectFileStates(repo.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s skipped %s: %v\n", time.Now().Format(time.RFC3339), repo.Path, err)
			continue
		}
		previous, initialized := snapshots[repo.Path]
		if !initialized {
			previous = repo.Files
		}
		if err := syncRepository(ctx, cfg, repo, previous, current); err != nil {
			return err
		}
		snapshots[repo.Path] = current
		if err := updateSnapshot(repo.Path, current); err != nil {
			return err
		}
	}
	return nil
}

func syncRepository(ctx context.Context, cfg *config.Config, repo Repository, previous, current map[string]indexer.FileState) error {
	store, err := db.InitStorage()
	if err != nil {
		return err
	}
	defer store.Close()
	idx, err := indexer.New(store, cfg, repo.Path, repo.NoEmbeddings)
	if err != nil {
		return err
	}
	for relativePath := range previous {
		if _, exists := current[relativePath]; !exists {
			if err := idx.RemoveFile(ctx, relativePath); err != nil {
				return fmt.Errorf("remove %s: %w", relativePath, err)
			}
			fmt.Printf("%s removed %s from %s\n", time.Now().Format(time.RFC3339), relativePath, repo.Path)
		}
	}
	for relativePath, state := range current {
		if oldState, exists := previous[relativePath]; exists && oldState == state {
			continue
		}
		stats, err := idx.SyncFile(ctx, filepath.Join(repo.Path, filepath.FromSlash(relativePath)))
		if err != nil {
			return fmt.Errorf("sync %s: %w", relativePath, err)
		}
		fmt.Printf("%s synced %s in %s (%d nodes, %d embeddings)\n", time.Now().Format(time.RFC3339), relativePath, repo.Path, stats.NodesSaved, stats.EmbeddingsCreated)
	}
	return nil
}

func load() (Registry, Paths, error) {
	paths, err := RuntimePaths()
	if err != nil {
		return Registry{}, Paths{}, err
	}
	data, err := os.ReadFile(paths.Registry)
	if errors.Is(err, os.ErrNotExist) {
		return Registry{}, paths, nil
	}
	if err != nil {
		return Registry{}, Paths{}, err
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return Registry{}, Paths{}, fmt.Errorf("read sync registry: %w", err)
	}
	return registry, paths, nil
}

func save(path string, registry Registry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func updateSnapshot(repoPath string, files map[string]indexer.FileState) error {
	registry, paths, err := load()
	if err != nil {
		return err
	}
	for idx := range registry.Repositories {
		if samePath(registry.Repositories[idx].Path, repoPath) {
			registry.Repositories[idx].Files = files
			return save(paths.Registry, registry)
		}
	}
	return nil
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
