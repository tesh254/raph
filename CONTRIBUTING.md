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

## Release Flow

- Merges to `main` trigger `.github/workflows/version-tag.yml`
- Workflow computes next semantic version and pushes tag
- Tag then triggers `.github/workflows/release.yml`
- GoReleaser publishes release artifacts and updates cask metadata

Manual tags still work:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Docs

- Update `README.md` when commands, release behavior, or install flow changes
- Update `docs/` when public usage docs or landing page content changes

