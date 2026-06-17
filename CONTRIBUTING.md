# Contributing

## Setup

```sh
go test ./...
go build ./cmd/raph
```

Use Go version from `go.mod`.

## Commit Style

Release automation on `main` reads commit messages to decide next semantic version.

- `BREAKING CHANGE` or `type!:` -> major
- `feat:` -> minor
- `fix:`, `perf:`, `refactor:` -> patch
- anything else -> patch fallback

Examples:

- `feat: add studio overview analytics`
- `fix: handle empty sqlite table rows`
- `refactor: simplify sync worker lifecycle`
- `feat!: change MCP response format`

## Pull Requests

- Branch from `main`
- Keep changes focused
- Run tests before opening PR
- Include manual verification notes for UI, sync, or release-flow changes
- Document user-facing command, MCP tool, schema, storage, or Studio behavior changes
- Call out destructive operations, migrations, background workers, and network calls in the PR description

## Review Checklist

- `go test ./...` passes
- `go build ./cmd/raph` passes
- New MCP tools or arguments are documented in `README.md`
- Database schema changes are backward compatible for existing `~/.raph/data/raph.db` files or include a migration path
- Background workers shut down cleanly and avoid duplicate long-running processes
- Networked features have bounded crawl/search limits and work without an embedding provider when possible
- Studio changes keep destructive actions explicit and local-only
- No real API keys, tokens, or local absolute paths are committed

## Release Flow

- Merges to `main` trigger `.github/workflows/version-tag.yml`
- Workflow computes next semantic version and pushes tag
- The same workflow then calls `.github/workflows/release.yml` so action-created tags still publish a release
- Manual tag pushes still trigger `.github/workflows/release.yml`
- GoReleaser publishes release artifacts and syncs cask metadata to `tesh254/homebrew-raph`
- `HOMEBREW_TAP_GITHUB_TOKEN` must have write access to both `tesh254/raph` and `tesh254/homebrew-raph`

Manual tags still work:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Docs

- Update `README.md` when commands, release behavior, or install flow changes
- Update `docs/` when public usage docs or landing page content changes
