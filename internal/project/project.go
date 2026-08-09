// Package project resolves a directory on disk to a stable project identity.
//
// Every durable memory that belongs to a codebase is scoped by this identity,
// and the indexer tags its graph nodes with it. Both sides must agree, so the
// derivation lives here rather than being repeated per caller.
package project

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"raph/internal/config"
)

// IDPrefix marks a project identity string. Identities are "project:<sha1>",
// where the digest is over whichever anchor identified the project.
const IDPrefix = "project:"

// Anchor names what an identity was derived from. It is reported so tooling can
// explain why two checkouts did or did not resolve to the same project.
const (
	// AnchorRemote is the strongest anchor: the repository's origin URL. It
	// survives moving, renaming, and re-cloning the checkout.
	AnchorRemote = "remote"
	// AnchorWorktree is the git worktree root, used when a repository has no
	// usable remote. Moving the checkout changes the identity.
	AnchorWorktree = "worktree"
	// AnchorDirectory is the directory itself, used outside git.
	AnchorDirectory = "directory"
	// AnchorOverride is project.identity_override from config.
	AnchorOverride = "override"
)

// Identity is a resolved project: the id memories are scoped by, plus enough
// context to show a person or an agent why it resolved that way.
type Identity struct {
	ID   string `json:"id"`
	Root string `json:"root"`
	Name string `json:"name"`
	// Anchor is one of the Anchor* constants.
	Anchor string `json:"anchor"`
	// Source is the value that was hashed: a canonical remote
	// ("github.com/owner/repo") or the path, depending on Anchor.
	Source string `json:"source"`
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
			return Identity{
				ID: IDPrefix + override, Root: root, Name: override,
				Anchor: AnchorOverride, Source: override,
			}, nil
		}
	}

	root, err := resolveRoot(dir)
	if err != nil {
		return Identity{}, err
	}

	// A repository is identified by what it *is*, not where it happens to sit.
	// Hashing the checkout path means moving or re-cloning a repo silently
	// orphans every memory ever written for it, and two clones of one project
	// never see each other's knowledge. The origin URL survives all of that.
	if remote, ok := canonicalRemote(root); ok {
		return Identity{
			ID: idFor(remote), Root: root, Name: filepath.Base(root),
			Anchor: AnchorRemote, Source: remote,
		}, nil
	}

	// No usable remote: fall back to the path, which is exactly what identities
	// were before remotes were used — so a repository without one keeps the id
	// its existing memories are already stored under.
	anchor := AnchorWorktree
	if !insideGitWorktree(root) {
		anchor = AnchorDirectory
	}
	return Identity{
		ID: idFor(root), Root: root, Name: filepath.Base(root),
		Anchor: anchor, Source: root,
	}, nil
}

// LegacyPathID returns the identity a root would have had before remotes
// anchored them. The migration uses it to find memories written under the old
// scheme; nothing else should.
func LegacyPathID(root string) string { return idFor(root) }

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

// canonicalRemote returns a stable "host/path" identity for a repository's
// origin, or false when there is no remote it can identify a project by.
//
// Everything that varies between two clones of the same repository is stripped:
// the transport (ssh vs https), any embedded credentials, the port, a trailing
// ".git", and case. What remains — host plus repository path — is the same
// string no matter who cloned it or where.
func canonicalRemote(root string) (string, bool) {
	raw, err := gitRemoteURL(root)
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return canonicalizeRemoteURL(raw)
}

func canonicalizeRemoteURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	var hostPath string
	switch {
	case strings.Contains(raw, "://"):
		// scheme://[user[:password]@]host[:port]/path
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return "", false
		}
		// file:// names a location on disk, exactly like a bare path remote —
		// "file://localhost/srv/git/x" even carries a host, so it would
		// otherwise sail through as though it identified a repository.
		if strings.EqualFold(parsed.Scheme, "file") {
			return "", false
		}
		// Hostname() drops the port; User is discarded entirely so a token
		// embedded in a remote URL can never end up inside an identity.
		hostPath = parsed.Hostname() + "/" + strings.TrimPrefix(parsed.Path, "/")
	case strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "."):
		// A filesystem remote identifies a location, not a project — exactly
		// what anchoring to a remote is meant to avoid.
		return "", false
	default:
		// scp-like: [user@]host:path
		at := strings.LastIndex(raw, "@")
		rest := raw[at+1:]
		colon := strings.Index(rest, ":")
		if colon <= 0 || colon+1 >= len(rest) {
			return "", false
		}
		hostPath = rest[:colon] + "/" + strings.TrimPrefix(rest[colon+1:], "/")
	}

	// Lowercase BEFORE stripping the suffix: hosts are case-insensitive and
	// forges treat owner/repo that way too, and a remote written ".GIT" would
	// otherwise keep its suffix and become a second, unrelated project.
	hostPath = strings.ToLower(strings.TrimSpace(hostPath))
	hostPath = strings.TrimSuffix(hostPath, "/")
	hostPath = strings.TrimSuffix(hostPath, ".git")
	hostPath = strings.Trim(hostPath, "/")

	// Require host and at least one path segment; a bare host names no project.
	if !strings.Contains(hostPath, "/") {
		return "", false
	}
	return hostPath, true
}

// gitRemoteURL prefers origin and falls back to the first configured remote, so
// a repository whose remote is named differently is still identified by it.
func gitRemoteURL(root string) (string, error) {
	if out, err := runGit(root, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(out) != "" {
		return out, nil
	}
	names, err := runGit(root, "remote")
	if err != nil {
		return "", err
	}
	for _, name := range strings.Fields(names) {
		if out, err := runGit(root, "remote", "get-url", name); err == nil && strings.TrimSpace(out) != "" {
			return out, nil
		}
	}
	return "", errNoRemote
}

var errNoRemote = errors.New("no git remote")

func insideGitWorktree(root string) bool {
	out, err := runGit(root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
