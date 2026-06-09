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
- GoReleaser releases for macOS, Linux, and Windows
- verified POSIX and PowerShell installers
- an in-repository Homebrew tap
- automatic stable-version updates
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

To skip embeddings during indexing:

```sh
raph init --path . --no-embeddings
```

### 3. Run the MCP server

```sh
raph start
```

Coding agents connected over MCP can use:

- `memory_store` and `memory_delete` for durable memory
- `hybrid_semantic_search` to retrieve memory, code, and documentation
- `multi_query_search` to retrieve results for several queries in one call
- `best_vector_match` to return the single closest semantic match
- `crawl_url` to fetch and embed exactly one user-provided link
- `crawl_website` to crawl a site and return compact excerpts relevant to a question
- `index_codebase` to index the agent's current codebase or another local repository
- `graph_neighbors` to traverse related graph nodes

Every node has a stable unique `id`. Nodes indexed from a local repository also expose the absolute codebase `path`, allowing agents to prefer results from the repository they are currently working in. Re-index existing repositories once to populate `path` on nodes created before this field was added.

`index_codebase` defaults to the MCP server working directory when `path` is omitted. It replaces existing indexed nodes for that codebase. Pass `no_embeddings: true` to index without remote embedding calls.

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

Then visit `http://localhost:4545`.

## Commands

```text
raph init            Scan a workspace and build graph relationships
raph start           Start the MCP server over stdio
raph studio          Launch the local graph explorer UI
raph sync            Index and continuously synchronize a repository
raph sync --status   Show the worker and registered repositories
raph sync --remove   Unregister a repository and clean its graph data
raph sync --stop     Stop the background sync worker
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

Installed release builds check for a newer stable release at most once every 24 hours and replace themselves when one exists. Development builds do not check. Disable automatic checks with:

```sh
export RAPH_NO_AUTO_UPDATE=1
```

Run an update immediately with:

```sh
raph update
```

## Build locally

```sh
go test ./...
go build ./cmd/raph
```

## Release

Releases use semantic version tags. Create and push a tag such as:

```sh
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` validates the tag, runs tests, and runs GoReleaser. GoReleaser publishes release archives, `checksums.txt`, changelog notes, and the Homebrew cask in this repository.

Validate release config locally:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

## Release artifacts

GoReleaser builds these targets:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

Usage docs deploy from `docs/` to `https://tesh254.github.io/raph/`.
