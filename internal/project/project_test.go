package project

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"raph/internal/config"
)

// TestIdentityHashIsStable pins the on-disk identity format. Every memory raph
// has ever written is scoped by these ids, so a change here orphans them: the
// digest stays sha1 over the resolved root, prefixed with "project:".
func TestIdentityHashIsStable(t *testing.T) {
	const root = "/Users/wchr/knnls/raph"
	sum := sha1.Sum([]byte(root))
	want := "project:" + hex.EncodeToString(sum[:])

	if got := idFor(root); got != want {
		t.Fatalf("identity format changed: got %s, want %s", got, want)
	}
	if want != "project:4ca6a8dbc1fee8c6ab5e4bca970b7a2607a07013" {
		t.Fatalf("sha1 identity for %s changed: %s", root, want)
	}
}

func TestResolveUsesGitRootForSubdirectories(t *testing.T) {
	repo := initRepo(t)
	nested := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	top, err := Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	deep, err := Resolve(nil, nested)
	if err != nil {
		t.Fatal(err)
	}

	if top.ID != deep.ID {
		t.Fatalf("subdirectory resolved to a different project: %s vs %s", top.ID, deep.ID)
	}
	if top.Root != deep.Root {
		t.Fatalf("expected both to report the worktree root, got %s and %s", top.Root, deep.Root)
	}
	if top.Name != filepath.Base(top.Root) {
		t.Fatalf("expected name %q, got %q", filepath.Base(top.Root), top.Name)
	}
}

// A project reached through a symlink must resolve to the identity of its real
// path — otherwise memories written from one path are invisible from the other.
func TestResolveFollowsSymlinks(t *testing.T) {
	repo := initRepo(t)
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	direct, err := Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	viaLink, err := Resolve(nil, link)
	if err != nil {
		t.Fatal(err)
	}
	if direct.ID != viaLink.ID {
		t.Fatalf("symlinked path resolved differently: %s vs %s", direct.ID, viaLink.ID)
	}
}

func TestResolveTrailingSeparatorIsSameProject(t *testing.T) {
	repo := initRepo(t)

	plain, err := Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	trailing, err := Resolve(nil, repo+string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if plain.ID != trailing.ID {
		t.Fatalf("trailing separator produced a second identity: %s vs %s", plain.ID, trailing.ID)
	}
}

// Outside a git worktree the directory itself is the project root, so sibling
// directories stay distinct projects.
func TestResolveWithoutGitUsesDirectory(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	idA, err := Resolve(nil, a)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := Resolve(nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if idA.ID == idB.ID {
		t.Fatal("expected sibling non-git directories to be distinct projects")
	}
	resolvedA, _ := filepath.EvalSymlinks(a)
	if idA.Root != resolvedA {
		t.Fatalf("expected root %s, got %s", resolvedA, idA.Root)
	}
}

// The override pins one identity regardless of where the project is checked
// out, which is its entire purpose.
func TestResolveIdentityOverrideWinsOverPath(t *testing.T) {
	cfg := &config.Config{Project: config.ProjectSettings{IdentityOverride: "acme-monolith"}}

	first, err := Resolve(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "project:acme-monolith" {
		t.Fatalf("expected overridden identity, got %s", first.ID)
	}
	if first.ID != second.ID {
		t.Fatal("expected the override to pin one identity across directories")
	}
}

func TestResolveEmptyDirUsesProcessWorkingDirectory(t *testing.T) {
	identity, err := Resolve(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(identity.ID, IDPrefix) {
		t.Fatalf("expected a %s identity, got %s", IDPrefix, identity.ID)
	}
	if identity.Root == "" {
		t.Fatal("expected a resolved root for the process working directory")
	}
}

func TestIDMatchesResolve(t *testing.T) {
	dir := t.TempDir()
	identity, err := Resolve(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ID(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if id != identity.ID {
		t.Fatalf("ID and Resolve disagree: %s vs %s", id, identity.ID)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v (%s)", err, out)
	}
	return dir
}

// Every clone of one repository must canonicalize to the same string, whatever
// transport, credentials, port, casing, or .git suffix the remote carries —
// that equality is the whole point of anchoring identity to the remote.
func TestCanonicalizeRemoteURL(t *testing.T) {
	same := []string{
		"git@github.com:tesh254/raph.git",
		"git@github.com:tesh254/raph",
		"https://github.com/tesh254/raph.git",
		"https://github.com/tesh254/raph",
		"ssh://git@github.com/tesh254/raph.git",
		"ssh://git@github.com:2222/tesh254/raph.git",
		"https://GitHub.com/Tesh254/Raph.git",
		"git://github.com/tesh254/raph.git",
		"https://github.com/tesh254/raph/",
		"https://github.com/tesh254/raph.GIT",
		"GIT@GITHUB.COM:tesh254/raph.Git",
		// A token in a remote URL must never reach an identity.
		"https://tesh254:ghp_secret@github.com/tesh254/raph.git",
	}
	for _, raw := range same {
		got, ok := canonicalizeRemoteURL(raw)
		if !ok {
			t.Fatalf("canonicalizeRemoteURL(%q) reported no remote", raw)
		}
		if got != "github.com/tesh254/raph" {
			t.Fatalf("canonicalizeRemoteURL(%q) = %q, want github.com/tesh254/raph", raw, got)
		}
		if strings.Contains(got, "ghp_secret") || strings.Contains(got, "@") {
			t.Fatalf("credentials leaked into identity source: %q", got)
		}
	}

	distinct := map[string]string{
		"git@github.com:tesh254/other.git":  "github.com/tesh254/other",
		"git@gitlab.com:tesh254/raph.git":   "gitlab.com/tesh254/raph",
		"https://git.example.com/a/b/c.git": "git.example.com/a/b/c",
	}
	for raw, want := range distinct {
		got, ok := canonicalizeRemoteURL(raw)
		if !ok || got != want {
			t.Fatalf("canonicalizeRemoteURL(%q) = %q (%t), want %q", raw, got, ok, want)
		}
	}

	// Remotes that name a location rather than a project must be rejected, so
	// the caller falls back to path identity instead of inventing a worse one.
	for _, raw := range []string{
		"", "   ", "/srv/git/raph.git", "./mirror.git", "../x", "github.com", "https://github.com",
		// file:// carries a host but still names a location on disk.
		"file:///srv/git/raph.git", "file://localhost/srv/git/raph.git", "FILE://localhost/srv/git/raph.git",
	} {
		if got, ok := canonicalizeRemoteURL(raw); ok {
			t.Fatalf("canonicalizeRemoteURL(%q) unexpectedly produced %q", raw, got)
		}
	}
}

// The point of the change: a repository keeps its identity when it moves.
func TestResolveIdentitySurvivesMovingTheCheckout(t *testing.T) {
	first := initRepoWithRemote(t, "git@github.com:acme/widget.git")
	second := initRepoWithRemote(t, "https://github.com/acme/widget.git")

	a, err := Resolve(nil, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(nil, second)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("two clones of one repository resolved differently: %s vs %s", a.ID, b.ID)
	}
	if a.Anchor != AnchorRemote || a.Source != "github.com/acme/widget" {
		t.Fatalf("expected a remote-anchored identity, got anchor=%s source=%s", a.Anchor, a.Source)
	}
	// The roots genuinely differ — this is the moved-checkout case.
	if a.Root == b.Root {
		t.Fatal("fixture error: both checkouts share a root")
	}
}

func TestResolveDistinctRemotesAreDistinctProjects(t *testing.T) {
	a, err := Resolve(nil, initRepoWithRemote(t, "git@github.com:acme/widget.git"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(nil, initRepoWithRemote(t, "git@github.com:acme/gadget.git"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("different repositories collapsed into one project")
	}
}

// A repository with no remote must keep the identity it already had, so
// existing memories stay attached without any migration.
func TestResolveWithoutRemoteKeepsLegacyPathIdentity(t *testing.T) {
	repo := initRepo(t)

	identity, err := Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Anchor != AnchorWorktree {
		t.Fatalf("expected a worktree anchor, got %s", identity.Anchor)
	}
	if identity.ID != LegacyPathID(identity.Root) {
		t.Fatalf("a remoteless repository changed identity: %s vs legacy %s", identity.ID, LegacyPathID(identity.Root))
	}
}

func TestResolveOutsideGitReportsDirectoryAnchor(t *testing.T) {
	dir := t.TempDir()
	identity, err := Resolve(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Anchor != AnchorDirectory {
		t.Fatalf("expected a directory anchor outside git, got %s", identity.Anchor)
	}
	if identity.ID != LegacyPathID(identity.Root) {
		t.Fatalf("non-git directory changed identity: %s", identity.ID)
	}
}

// A subdirectory of a remote-anchored repository still resolves to the repo.
func TestResolveSubdirectoryOfRemoteAnchoredRepo(t *testing.T) {
	repo := initRepoWithRemote(t, "git@github.com:acme/widget.git")
	nested := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	top, err := Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	deep, err := Resolve(nil, nested)
	if err != nil {
		t.Fatal(err)
	}
	if top.ID != deep.ID {
		t.Fatalf("subdirectory resolved to a different project: %s vs %s", deep.ID, top.ID)
	}
}

func TestResolveOverrideStillWinsOverRemote(t *testing.T) {
	cfg := &config.Config{Project: config.ProjectSettings{IdentityOverride: "acme-monolith"}}
	identity, err := Resolve(cfg, initRepoWithRemote(t, "git@github.com:acme/widget.git"))
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != "project:acme-monolith" || identity.Anchor != AnchorOverride {
		t.Fatalf("override did not win: %+v", identity)
	}
}

func initRepoWithRemote(t *testing.T, remote string) string {
	t.Helper()
	dir := initRepo(t)
	cmd := exec.Command("git", "remote", "add", "origin", remote)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v (%s)", err, out)
	}
	return dir
}
