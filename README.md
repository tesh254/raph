# raph

`raph` is a local-first graph-vector brain for coding agents. It indexes local code, docs, and notes into a relational graph stored under `~/.raph/`, exposes search over MCP/stdin-stdout, and includes a zero-dependency Studio UI for graph inspection.

## Current shape

This repository now includes:

- a Go CLI in `cmd/raph`
- config bootstrapping in `internal/config`
- an embedded local database in `internal/db`
- a workspace indexer in `internal/indexer`
- a cross-platform background repository synchronizer in `internal/syncer`
- an MCP server in `internal/mcp`
- a zero-dependency Studio UI in `internal/studio`
- scoped durable memory with lifecycle metadata for agent preferences, facts, procedures, and project knowledge
- codebase chunk indexing for non-Go files so README, docs, config, and other text assets are searchable alongside symbols
- GoReleaser releases for macOS, Linux, and Windows
- verified POSIX and PowerShell installers
- an in-repository Homebrew tap
- explicit stable-version updates with optional opt-in auto-update
- detached minisign signatures for release checksums
- GitHub artifact attestations and `gh` verification hooks for releases
- a GitHub Pages usage site

## Why the storage layer is SQLite-compatible right now

The original PRD called for embedded libSQL/Turso storage. For this first implementation, the project uses a pure-Go embedded SQLite-compatible backend with in-process cosine search so the binary can compile cleanly on macOS, Linux, and Windows from day one.

That keeps the architecture local-first and single-binary while avoiding the current upstream platform limitations in `go-libsql` for Windows builds.

## Quick start

### 1. Create config files

```sh
raph config init
```

This creates:

- `~/.raph/schema.json`
- `~/.raph/raph.json`
- `~/.raph/data/`

Then set your OpenRouter key:

```sh
export OPENROUTER_API_KEY="your-key"
```

### 2. Index a repository

```sh
raph init --path .
```

Then install project MCP entries for supported coding agents:

```sh
raph agents mcp setup --path .
```

To skip embeddings during indexing:

```sh
raph init --path . --no-embeddings
```

`init` also registers the repository for background synchronization. The worker keeps supported files in sync, updates changed nodes, and removes graph data for deleted files.

### 3. Run the MCP server

```sh
raph start
```

Coding agents connected over MCP can use:

- `store_memory`, `update_memory`, `deprecate_memory`, and memory lookup tools for durable scoped memory
- `hybrid_semantic_search` to retrieve memory, code, and documentation
- `multi_query_search` to retrieve results for several queries in one call
- `best_vector_match` to return the single closest semantic match
- `crawl_url` to fetch and embed exactly one user-provided link
- `crawl_website` to crawl a site and return compact excerpts relevant to a question
- `index_codebase` to index the agent's current codebase or another local repository
- `graph_neighbors` to traverse related graph nodes

`store_memory` accepts scoped memory metadata so agents can distinguish project, shared, and global knowledge. Memories include lifecycle state, knowledge type, source, writer, revision, tags, and timestamps. `update_memory` preserves previous revisions, while `deprecate_memory` retires stale records without deleting unrelated graph nodes.

For repo handoff between agents, use project scope and a stable `memory_key`:

```json
{
  "scope_type": "project",
  "knowledge_type": "workflow",
  "memory_key": "release/signing",
  "title": "Release signing workflow",
  "content": "This repo uses minisign signatures, immutable GitHub releases, and verifies release artifacts with `gh release verify`.",
  "source": "conversation",
  "writer_id": "agent:opencode",
  "tags": ["release", "signing", "github"]
}
```

When `scope_type` is `project`, agents may omit `scope_id`; raph resolves it from the current workspace and `project.identity_override` when configured. Agents should call `search_project_knowledge` at the start of work and store or update durable decisions, setup facts, gotchas, commands, and constraints before finishing. Use keys such as `repo/setup`, `release/signing`, `ci/known-issues`, and `agent/constraints`.

Every node has a stable unique `id`. Nodes indexed from a local repository also expose the absolute codebase `path`, allowing agents to prefer results from the repository they are currently working in. Re-index existing repositories once to populate `path` on nodes created before this field was added.

`index_codebase` defaults to the MCP server working directory when `path` is omitted. It replaces existing indexed nodes for that codebase. Pass `no_embeddings: true` to index without remote embedding calls. Go files are indexed as symbols and relationships; supported non-Go files are indexed as chunk nodes connected to their source file.

`crawl_website` returns at most 3 compact matches by default, with excerpts capped at 600 characters. Agents can request up to 10 matches and 2,000 characters per excerpt when more context is needed.

To ingest only one page from the CLI:

```sh
raph crawl --single https://example.com/docs/page
```

### 4. Keep a repository synchronized

```sh
raph sync --path .
```

`sync` performs an initial index, registers the absolute repository path, and starts one detached background worker. The worker detects supported file changes across all registered repositories, replaces changed file nodes, and removes nodes for deleted files.

```sh
raph sync --status
raph sync --remove --path .
raph sync --stop
```

Removing a repository also removes its graph data by default. Pass `--keep-data` to unregister it without cleaning its nodes.

### 5. Open Studio

```sh
raph studio --port 4545
```

Studio now binds to `127.0.0.1` by default. Then visit `http://localhost:4545`.

To expose Studio on another interface, pass `--host` explicitly:

```sh
raph studio --host 0.0.0.0 --port 4545
```

Studio exposes graph browsing, keyword/semantic search, neighbor expansion, node details, SQLite table inspection, and local-only maintenance actions. The init action clears the local database, indexes the current workspace, and crawls the configured seed URL. The clear action wipes local graph data. Use Studio only on trusted local machines.

For operational troubleshooting, pass `--verbose` to any command:

```sh
raph --verbose start
```

## Commands

```text
raph init            Scan a workspace and build graph relationships
raph start           Start the MCP server over stdio
raph studio          Launch the local graph explorer UI
raph agents mcp setup Install or refresh project MCP config for supported coding agents
raph sync            Index and continuously synchronize a repository
raph sync --status   Show the worker and registered repositories
raph sync --remove   Unregister a repository and clean its graph data
raph sync --stop     Stop the background sync worker
raph clear --yes     Clear all local graph data
raph config init     Create ~/.raph/schema.json and ~/.raph/raph.json
raph config path     Print config/data paths
raph config check    Validate the current config file
raph update          Install the latest stable release
raph version         Print version, commit, and build date
```

## Install from a release

### curl

```sh
curl -fsSL https://raw.githubusercontent.com/tesh254/raph/main/install.sh | sh
```

### wget

```sh
wget -qO- https://raw.githubusercontent.com/tesh254/raph/main/install.sh | sh
```

You can override the repository or version:

```sh
RAPH_REPO=tesh254/raph RAPH_VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/tesh254/raph/main/install.sh | sh
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/tesh254/raph/main/install.ps1 | iex
```

### Homebrew

This repository is its own tap. GoReleaser writes `Casks/raph.rb` on each stable release.

```sh
brew install --cask tesh254/raph/raph
```

## Updates

Installed release builds update only when you run `raph update`. Development builds do not self-update.

To opt into automatic update attempts before normal commands, set:

```sh
export RAPH_AUTO_UPDATE=1
```

Run an update immediately with:

```sh
raph update
```

`update` now requires the release checksum file to be signed by the bundled minisign public key before any archive is trusted or applied.

## Build locally

```sh
go test ./...
go build ./cmd/raph
```

Before merging feature work, run:

```sh
go test ./...
go build ./cmd/raph
```

For changes touching Studio, also smoke test:

```sh
raph studio --port 4545
```

For changes touching MCP behavior, start the stdio server from a configured agent or run:

```sh
raph start
```

## Release

Before publishing the first hardened release, enable immutable releases once:

```sh
./scripts/check-immutable-releases.sh
./scripts/enable-immutable-releases.sh
```

The release workflow also requires these repository secrets:

- `RAPH_MINISIGN_PRIVATE_KEY`
- `RAPH_MINISIGN_PASSWORD`

Merges to `main` now auto-create and push a semantic version tag through `.github/workflows/version-tag.yml`.

Bump rules follow conventional commits across commits since the previous tag:

- `BREAKING CHANGE` or `type!:` -> major
- `feat:` -> minor
- `fix:`, `perf:`, or `refactor:` -> patch
- anything else falls back to patch so merged work still ships

`.github/workflows/version-tag.yml` now calls `.github/workflows/release.yml` immediately after pushing the tag, because tags created by GitHub Actions do not start a second workflow run on their own. Direct manual tag pushes still trigger `.github/workflows/release.yml`.

`.github/workflows/release.yml` validates the generated tag, requires immutable releases, runs tests, signs `checksums.txt`, publishes release archives, generates GitHub artifact attestations, verifies the finished release with `gh`, and updates the Homebrew cask in this repository.

Manual tags still work when needed:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Validate release config locally:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

Verify a published release with GitHub CLI:

```sh
gh release verify v0.1.1
gh release download v0.1.1 --pattern 'raph_linux_amd64.tar.gz' --pattern 'checksums.txt' --pattern 'checksums.txt.minisig'
./scripts/verify-release.sh v0.1.1 ./raph_linux_amd64.tar.gz ./checksums.txt ./checksums.txt.minisig
```

## Release artifacts

GoReleaser builds these targets:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

Usage docs deploy from `docs/` to `https://raph.madebyknnls.com/`.

## License

Raph is source-available for private, non-commercial, personal use only.
Commercial use, business use, monetization, selling, and reselling are
prohibited without a separate written license. See [LICENSE](LICENSE) for the
full terms.
