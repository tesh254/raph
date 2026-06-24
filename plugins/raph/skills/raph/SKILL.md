---
name: raph
description: Use raph — a local-first graph brain — for shared memory, rules, local documentation, handoffs, wikis, and fast code/doc search. Activate when you need to remember durable project facts, record or read rules, store/look up local docs, hand work off to another agent, or search a codebase without running ad-hoc bash greps. Prefer the raph MCP server when it is connected; otherwise drive raph through these CLI commands.
---

# raph

raph keeps a local-first knowledge graph of your codebases plus shared memory,
rules, and documents. Prefer the **raph MCP server** when connected (tools:
`search`, `store_memory`/`update_memory`, `store_rule`/`list_rules`,
`add_document`/`read_document`/`list_documents`/`link_nodes`,
`search_project_knowledge`/`search_shared_knowledge`/`search_global_preferences`,
`crawl_url`/`crawl_website`, `index_codebase`/`search_codebase`,
`graph_neighbors`). If no MCP is available, use the CLI below.

All commands accept `--format json` for machine-readable output (this is the
default when raph detects it is being called by an agent or through a pipe). Add
`--format text` for human-readable output.

## Index & keep a codebase current
```bash
raph init --path .        # index this repo and arm the realtime watcher
raph sync --path .        # ensure background sync is running
```
The background watcher refreshes the graph within ~150ms of a file save, so the
graph is always current — you do not need to re-index manually.

## Search code & docs (ripgrep-style, no raph syntax to learn)
```bash
raph search "database connection" --format json        # ranked keyword (bm25)
raph search "ResponseWriter" --literal                  # exact substring
raph search "Open[A-Z]\w+" --regex                       # regular expression
raph search "auth" --type func --type type --limit 20    # filter by node type
raph search "config loader" --vector                     # semantic (needs provider)
raph search "TODO" --global                              # across all workspaces
```
Each match returns id, type, name, url (path#symbol), and an excerpt. Use this
instead of shelling out to grep/rg for indexed content.

## Memory — project, shared, or global scope
Project memory affects only this codebase; global memory affects all your work;
shared memory is keyed by an explicit id you pick.
```bash
raph mem set "We use modernc sqlite (no cgo)" --scope project --type decision --title "sqlite driver"
raph mem set "Prefer small PRs" --scope global --type preference --title "PR size"
raph mem search "sqlite" --scope project --format json
raph mem rm <node_id> --reason "superseded"
```

## Rules — global or codebase-specific
```bash
raph rules add "Always run go test before commit" --scope project
raph rules add "Prefer table-driven tests" --scope global
raph rules list --all          # both global and project rules
raph rules rm <node_id>
```
Read rules at the start of a task: global rules govern the whole dev cycle,
project rules govern this repo specifically.

## Local documents, wikis & handoffs
Documents carry a `doc_type` so you know how they serve the work:
`architecture` (durable design), `handoff` (work transfer), `reference` (a fact
to confirm against), or `note`.
```bash
raph doc add ./NOTES.md --type architecture --title "Service layout"
raph doc add "Finished FTS. Next: vector rerank." --type handoff --title "FTS handoff"
echo "facts..." | raph doc add - --type reference --title "API limits"
raph doc list --type handoff --format json
raph doc read <id>             # reading a handoff MARKS IT USED so the next agent skips it
raph doc read <id> --no-mark   # peek without claiming
raph doc link <from_id> <to_id> --rel RELATES_TO   # connect a doc to code/another doc
```
When you pick up a handoff, `raph doc read <id>` automatically flips its status
to `used` — another agent then knows to focus elsewhere. Documents are chunked
and linked, so follow relations (via `graph_neighbors` / the `related` field)
instead of re-searching.

## Pull documentation from the internet
```bash
raph crawl https://example.com/docs            # crawl a docs site into the graph
raph crawl https://example.com/page --single   # one page only
```
After crawling, the content is searchable with `raph search`.

## Export & transfer knowledge
```bash
raph export --doc <id> --out notes.md                 # to a file
raph export --doc <id> --gist --public                # publish as a GitHub gist
raph export --bundle --out-format json --out kb.json  # whole-workspace bundle
raph export --doc <id> --s3 s3://bucket/key --r2-endpoint https://<acct>.r2.cloudflarestorage.com
```

## Conventions
- Set `RAPH_WRITER=<your-agent-id>` so memory/rules/handoffs are attributed.
- Check rules and project memory before starting; record durable decisions,
  gotchas, and a handoff before finishing.
