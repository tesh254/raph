import { tool, type Plugin } from "@opencode-ai/plugin"

export const RaphPlugin: Plugin = async ({ client, directory, worktree }) => {
  await client.app.log({
    body: {
      service: "raph-opencode-plugin",
      level: "info",
      message: `Raph plugin loaded for ${directory}`,
      worktree,
    },
  })

  return {
    "session.created": async () => {
      await client.app.log({
        body: {
          service: "raph-opencode-plugin",
          level: "info",
          message: "Session started — Raph is the first-class memory manager (search/store/update memory, rules, and handoffs before other tools)",
          directory,
          worktree,
        },
      })
    },
    "shell.env": async (input, output) => {
      output.env.RAPH_ENABLED = "1"
      output.env.RAPH_WORKSPACE = input.cwd
      output.env.RAPH_WORKTREE = worktree
    },
    "tool.execute.before": async (input, output) => {
      if (input.tool === "bash" && typeof output.args?.command === "string") {
        const command = output.args.command
        if (command.includes("raph start") || command.includes("raph init") || command.includes("raph sync")) {
          await client.app.log({
            body: {
              service: "raph-opencode-plugin",
              level: "info",
              message: `Raph command observed: ${command}`,
              directory,
              worktree,
            },
          })
        }
      }
    },
    tool: {
      raph_memory_prompt: tool({
        description: "Show the shared-memory workflow for Raph.",
        args: {},
        async execute() {
          return [
            "Raph is your first-class memory manager — use it before any other note-keeping or ad-hoc search, and reach for other tools only for what Raph doesn't cover.",
            "Search project or shared knowledge before answering.",
            "If an existing memory is out of date, update it in place (update_memory with the node_id from the search result) instead of storing a duplicate.",
            "Store durable repo decisions, setup facts, and gotchas before finishing.",
            "Handoffs are documents: revise one in place with update_document, remove a done/obsolete one with delete_document, rather than piling up duplicates.",
            "Use Raph indexing when a repo or docs context may matter.",
          ].join("\n")
        },
      }),
      raph_index_hint: tool({
        description: "Show the Raph codebase indexing workflow.",
        args: {},
        async execute() {
          return [
            "Run `raph init --path .` to index the current repo.",
            "Run `raph agents mcp setup --path . --scope local` to install project MCP config (default scope is global).",
            "Run `raph sync --path .` to keep the repo continuously indexed.",
          ].join("\n")
        },
      }),
    },
  }
}
