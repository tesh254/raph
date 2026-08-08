// Package project resolves a directory on disk to a stable project identity.
//
// Every durable memory that belongs to a codebase is scoped by this identity,
// and the indexer tags its graph nodes with it. Both sides must agree, so the
// derivation lives here rather than being repeated per caller.
package project

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"raph/internal/config"
)

// IDPrefix marks a project identity string. Identities are "project:<sha1>",
// where the digest is over the resolved project root.
const IDPrefix = "project:"

// Identity is a resolved project: the id memories are scoped by, plus the root
// it was derived from so callers can show the agent what was matched.
type Identity struct {
	ID   string `json:"id"`
	Root string `json:"root"`
	Name string `json:"name"`
}

// Resolve maps a directory to its project identity.
//
// The directory is any path inside the project — the agent's working directory,
// typically. It resolves to the enclosing git worktree root when there is one,
// so every subdirectory of a repository (and every checkout path that reaches
// the same worktree) yields one identity instead of one per directory.
//
// Symlinks are evaluated first because the same project is routinely reached by
// two paths — /tmp vs /private/tmp on macOS, or a symlinked checkout — and an
// unresolved path would hash to an identity nothing else ever writes to.
//
// An empty dir means "the process's own working directory", which is only
// meaningful for CLI commands: an MCP server's cwd is wherever the agent
// happened to launch it, so MCP callers pass the directory explicitly.
func Resolve(cfg *config.Config, dir string) (Identity, error) {
	if cfg != nil {
		if override := strings.TrimSpace(cfg.Project.IdentityOverride); override != "" {
			// An override deliberately pins one identity across checkout paths,
			// so it names the project rather than hashing a location.
			root, err := resolveRoot(dir)
			if err != nil {
				root = ""
			}
			return Identity{ID: IDPrefix + override, Root: root, Name: override}, nil
		}
	}

	root, err := resolveRoot(dir)
	if err != nil {
		return Identity{}, err
	}
	return Identity{ID: idFor(root), Root: root, Name: filepath.Base(root)}, nil
}

// ID is Resolve for callers that only need the identity string.
func ID(cfg *config.Config, dir string) (string, error) {
	identity, err := Resolve(cfg, dir)
	if err != nil {
		return "", err
	}
	return identity.ID, nil
}

// idFor hashes an already-resolved root. sha1 is an identity digest here, not a
// security boundary; it is kept because every memory ever written by raph is
// scoped by these ids and rehashing would orphan them.
func idFor(root string) string {
	sum := sha1.Sum([]byte(root))
	return IDPrefix + hex.EncodeToString(sum[:])
}

// resolveRoot canonicalizes dir and walks up to the enclosing git worktree root.
func resolveRoot(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		dir = cwd
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve project root %q: %w", dir, err)
	}
	// A path that doesn't exist yet still resolves: EvalSymlinks fails on it,
	// and the cleaned absolute path is a stable enough identity.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	if gitTopLevel, err := gitRoot(abs); err == nil && gitTopLevel != "" {
		// git reports a path that is already symlink-resolved; clean it anyway so
		// a trailing separator can't produce a second identity for one worktree.
		return filepath.Clean(gitTopLevel), nil
	}
	return abs, nil
}

func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
