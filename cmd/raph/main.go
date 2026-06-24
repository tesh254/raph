package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"raph/internal/agentsetup"
	"raph/internal/config"
	"raph/internal/crawler"
	"raph/internal/db"
	"raph/internal/indexer"
	serverpkg "raph/internal/mcp"
	"raph/internal/memory"
	"raph/internal/output"
	"raph/internal/query"
	"raph/internal/signing"
	"raph/internal/studio"
	"raph/internal/syncer"
	"raph/internal/updater"
	"raph/internal/verbose"
	"raph/internal/version"

	"github.com/spf13/cobra"
)

const autoUpdateEnvVar = "RAPH_AUTO_UPDATE"

// outputFormatFlag holds the value of the root --format flag (text|json|auto).
// Empty means auto-detect (JSON for agents/pipes, text for terminals).
var outputFormatFlag string

func resolveFormat() output.Format { return output.Resolve(outputFormatFlag) }

// resolveWorkspaceID maps a filesystem path to its graph workspace id.
func resolveWorkspaceID(store db.GraphStore, cfg *config.Config, path string) (string, error) {
	idx, err := indexer.New(store, cfg, path, true)
	if err != nil {
		return "", err
	}
	return idx.WorkspaceID(), nil
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var verboseFlag bool
	var quietFlag bool
	rootCmd := &cobra.Command{
		Use:           "raph",
		Short:         "raph is a local-first graph-vector brain for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Full(),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if quietFlag {
				verbose.Set(false)
			} else {
				verbose.Set(true)
				_ = os.Setenv("RAPH_VERBOSE", "1")
				verbose.Printf("command=%s args=%v", cmd.CommandPath(), args)
			}
			if cmd.Name() == "update" || version.Version == "dev" || os.Getenv(autoUpdateEnvVar) != "1" || !updater.ShouldAutoCheck() {
				return
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			result, err := updater.Update(ctx, version.Version)
			if err == nil && result.Updated {
				fmt.Fprintf(cmd.ErrOrStderr(), "raph updated to %s; restart command to use it\n", result.Latest)
			}
		},
	}
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", true, "Print verbose operational logs (enabled by default)")
	rootCmd.PersistentFlags().BoolVarP(&quietFlag, "quiet", "q", false, "Suppress verbose operational logs")
	rootCmd.PersistentFlags().StringVar(&outputFormatFlag, "format", "", "Output format: text, json, or auto (default auto: json for agents/pipes)")

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newSearchCmd())
	rootCmd.AddCommand(newMemCmd())
	rootCmd.AddCommand(newRulesCmd())
	rootCmd.AddCommand(newCrawlCmd())
	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newStudioCmd())
	rootCmd.AddCommand(newSyncCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newClearCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newUpdateCmd())
	rootCmd.AddCommand(newReleaseCmd())
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and build date",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			verbose.Printf("version=%s commit=%s date=%s", version.Version, version.Commit, version.Date)
			fmt.Fprintln(cmd.OutOrStdout(), version.Full())
		},
	})
	return rootCmd
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update raph to the latest stable release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Checking for updates...\n")
			verbose.Printf("current version=%s", version.Version)
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			result, err := updater.Update(ctx, version.Version)
			if err != nil {
				return err
			}
			if result.Updated {
				fmt.Fprintf(out, "Updated raph from %s to %s\n", result.Current, result.Latest)
				return nil
			}
			fmt.Fprintf(out, "raph %s is already current\n", result.Current)
			return nil
		},
	}
}

func newSyncCmd() *cobra.Command {
	var scanPath string
	var noEmbeddings bool
	var interval time.Duration
	var status bool
	var stop bool
	var remove bool
	var keepData bool
	var worker bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Keep indexed repositories synchronized in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if worker {
				verbose.Printf("starting sync worker in foreground mode interval=%s", interval)
				ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
				defer cancel()
				return syncer.RunWorker(ctx, interval)
			}
			if status {
				verbose.Printf("querying sync worker status")
				running, pid, err := syncer.Status()
				if err != nil {
					return err
				}
				repositories, err := syncer.List()
				if err != nil {
					return err
				}
				verbose.Printf("status: running=%t pid=%d repositories=%d", running, pid, len(repositories))
				if running {
					fmt.Fprintf(out, "Sync worker running (pid %d)\n", pid)
				} else {
					fmt.Fprintln(out, "Sync worker stopped")
				}
				for _, repo := range repositories {
					fmt.Fprintf(out, "%s (embeddings=%t)\n", repo.Path, !repo.NoEmbeddings)
				}
				return nil
			}
			if stop {
				verbose.Printf("stopping sync worker")
				stopped, err := syncer.Stop()
				if err != nil {
					return err
				}
				if stopped {
					fmt.Fprintln(out, "Stopped sync worker")
				} else {
					fmt.Fprintln(out, "Sync worker is not running")
				}
				return nil
			}
			if remove {
				verbose.Printf("removing repository from sync path=%s keepData=%t", scanPath, keepData)
				if err := syncer.Remove(context.Background(), scanPath, !keepData); err != nil {
					return err
				}
				fmt.Fprintf(out, "Stopped syncing %s (graph data removed=%t)\n", scanPath, !keepData)
				return nil
			}

			fmt.Fprintf(out, "Loading configuration...\n")
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Initializing local storage...\n")
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Preparing indexer for %s...\n", scanPath)
			idx, err := indexer.New(store, cfg, scanPath, noEmbeddings)
			if err != nil {
				_ = store.Close()
				return err
			}
			fmt.Fprintf(out, "Scanning and indexing files...\n")
			stats, err := idx.Run(context.Background())
			_ = store.Close()
			if err != nil {
				return err
			}
			verbose.Printf("index complete path=%s files=%d nodes=%d edges=%d embeddings=%d", scanPath, stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved, stats.EmbeddingsCreated)
			repo, err := syncer.Register(scanPath, noEmbeddings)
			if err != nil {
				return err
			}
			started, err := syncer.Start(interval)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Sync enabled for %s: %d files, %d nodes, %d edges\n", repo.Path, stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved)
			if started {
				fmt.Fprintln(out, "Background sync worker started")
			} else {
				fmt.Fprintln(out, "Background sync worker already running")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scanPath, "path", ".", "Repository path to sync")
	cmd.Flags().BoolVar(&noEmbeddings, "no-embeddings", false, "Skip remote embedding generation")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "File scan interval")
	cmd.Flags().BoolVar(&status, "status", false, "Show worker and repository status")
	cmd.Flags().BoolVar(&stop, "stop", false, "Stop the background sync worker")
	cmd.Flags().BoolVar(&remove, "remove", false, "Stop syncing the repository and remove its graph data")
	cmd.Flags().BoolVar(&keepData, "keep-data", false, "Keep graph data when removing a repository")
	cmd.Flags().BoolVar(&worker, "worker", false, "Run the sync worker in the foreground")
	_ = cmd.Flags().MarkHidden("worker")
	return cmd
}

func newInitCmd() *cobra.Command {
	var scanPath string
	var noEmbeddings bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scan a workspace and build graph relationships in local storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Loading configuration...\n")
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			if cfg == nil {
				fmt.Fprintf(out, "  No config found — embeddings will be skipped (keyword search still available)\n")
			} else if cfg.HasEmbeddingProvider() {
				fmt.Fprintf(out, "  Embedding provider: %s (model: %s)\n", cfg.Vector.CurrentProvider, cfg.Vector.Providers.OpenRouter.Model)
			} else {
				fmt.Fprintf(out, "  Config loaded but no embedding API key resolved — embeddings will be skipped\n")
			}

			fmt.Fprintf(out, "Initializing local storage...\n")
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			fmt.Fprintf(out, "Preparing indexer for %s...\n", scanPath)
			idx, err := indexer.New(store, cfg, scanPath, noEmbeddings)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "Scanning and indexing files...\n")
			stats, err := idx.Run(context.Background())
			if err != nil {
				return err
			}
			verbose.Printf("index complete path=%s files=%d nodes=%d edges=%d embeddings=%d", scanPath, stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved, stats.EmbeddingsCreated)
			repo, err := syncer.Register(scanPath, noEmbeddings)
			if err != nil {
				return err
			}
			started, err := syncer.Start(2 * time.Second)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\n")
			fmt.Fprintf(out, "Indexed %d files, %d nodes, %d edges, %d embeddings\n", stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved, stats.EmbeddingsCreated)
			fmt.Fprintf(out, "Watcher armed for %s\n", repo.Path)
			if started {
				fmt.Fprintln(out, "Background sync worker started")
			} else {
				fmt.Fprintln(out, "Background sync worker already running")
			}
			if !noEmbeddings && (cfg == nil || !cfg.HasEmbeddingProvider()) {
				fmt.Fprintln(out, "No resolved embedding provider key found; graph was indexed without embeddings. Run `raph config init` and set OPENROUTER_API_KEY to enable semantic search.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&scanPath, "path", ".", "Workspace path to index")
	cmd.Flags().BoolVar(&noEmbeddings, "no-embeddings", false, "Skip remote embedding generation during indexing")
	return cmd
}

func newSearchCmd() *cobra.Command {
	var scanPath string
	var global bool
	var types []string
	var limit int
	var literal bool
	var regex bool
	var vector bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the indexed graph (ripgrep-style) for code, docs, and knowledge",
		Long: "Search indexed nodes without learning raph query syntax. Defaults to ranked " +
			"keyword search; use --literal for exact substrings, --regex for patterns, or " +
			"--vector for semantic matches. Emits JSON for agents and text for terminals.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			workspace := ""
			if !global {
				workspace, err = resolveWorkspaceID(store, cfg, scanPath)
				if err != nil {
					return err
				}
			}

			mode := query.ModeAuto
			switch {
			case regex:
				mode = query.ModeRegex
			case vector:
				mode = query.ModeVector
			case literal:
				mode = query.ModeLiteral
			}

			result, err := query.Search(cmd.Context(), store, cfg, query.Options{
				Query:     strings.Join(args, " "),
				Workspace: workspace,
				Types:     types,
				Limit:     limit,
				Mode:      mode,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), result, func(w io.Writer) error {
				var sb strings.Builder
				result.RenderText(&sb)
				_, writeErr := io.WriteString(w, sb.String())
				return writeErr
			})
		},
	}
	cmd.Flags().StringVar(&scanPath, "path", ".", "Workspace path to scope the search")
	cmd.Flags().BoolVar(&global, "global", false, "Search across all indexed workspaces")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by node type (func, type, file, markdown_chunk, file_chunk, doc, doc_chunk)")
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of matches")
	cmd.Flags().BoolVar(&literal, "literal", false, "Exact substring match (ripgrep -F style)")
	cmd.Flags().BoolVar(&regex, "regex", false, "Treat the query as a regular expression")
	cmd.Flags().BoolVar(&vector, "vector", false, "Semantic search (requires a configured embedding provider)")
	return cmd
}

// writerID identifies who is writing memory/rules from the CLI. Agents can set
// RAPH_WRITER to attribute writes; otherwise a stable default is used.
func writerID() string {
	if w := strings.TrimSpace(os.Getenv("RAPH_WRITER")); w != "" {
		return w
	}
	return "cli"
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "entry"
	}
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
}

// resolveMemoryScope returns the (scopeType, scopeID) pair for a scope. project
// scope derives its id from the workspace's project identity; global uses a
// fixed bucket; shared requires an explicit id.
func resolveMemoryScope(cfg *config.Config, scope, scopeID, path string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	scopeID = strings.TrimSpace(scopeID)
	switch scope {
	case "project":
		if scopeID != "" {
			return scope, scopeID, nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", "", err
		}
		id, err := indexer.ResolveProjectIdentity(cfg, abs)
		if err != nil {
			return "", "", err
		}
		return scope, id, nil
	case "global":
		if scopeID == "" {
			scopeID = "global"
		}
		return scope, scopeID, nil
	case "shared":
		if scopeID == "" {
			return "", "", fmt.Errorf("--scope-id is required for shared scope")
		}
		return scope, scopeID, nil
	default:
		return "", "", fmt.Errorf("unknown scope %q (use project, shared, or global)", scope)
	}
}

func renderMemoryRecords(w io.Writer, records []db.MemoryRecord) error {
	if len(records) == 0 {
		_, err := io.WriteString(w, "No matching records\n")
		return err
	}
	var sb strings.Builder
	for _, r := range records {
		fmt.Fprintf(&sb, "%s  [%s/%s · %s]\n", r.Node.Name, r.ScopeType, r.KnowledgeType, r.LifecycleState)
		fmt.Fprintf(&sb, "    id: %s  key: %s\n", r.Node.ID, r.MemoryKey)
		if len(r.DisplayTags) > 0 {
			fmt.Fprintf(&sb, "    tags: %s\n", strings.Join(r.DisplayTags, ", "))
		}
		if c := strings.TrimSpace(r.Node.Content); c != "" {
			fmt.Fprintf(&sb, "    %s\n", query.NewExcerpt(c, 280))
		}
		sb.WriteString("\n")
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func newMemCmd() *cobra.Command {
	memCmd := &cobra.Command{
		Use:   "mem",
		Short: "Read and write scoped agent memory (project, shared, or global)",
	}

	var scope, scopeID, knowledgeType, title, key, path string
	var tags []string
	setCmd := &cobra.Command{
		Use:   "set <content>",
		Short: "Create or update a memory record (idempotent)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			scopeType, resolvedID, err := resolveMemoryScope(cfg, scope, scopeID, path)
			if err != nil {
				return err
			}
			content := strings.Join(args, " ")
			memKey := key
			if memKey == "" {
				if title != "" {
					memKey = slugify(title)
				} else {
					memKey = slugify(content)
				}
			}
			out, err := memory.Put(cmd.Context(), store, cfg, memory.StoreInput{
				ScopeType: scopeType, ScopeID: resolvedID, KnowledgeType: knowledgeType,
				Title: title, Content: content, Source: "cli", WriterID: writerID(), Tags: tags, MemoryKey: memKey,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), out, func(w io.Writer) error {
				fmt.Fprintf(w, "Saved %s memory %q (id %s, embedded=%t)\n", scopeType, out.Record.MemoryKey, out.Record.Node.ID, out.Embedded)
				return nil
			})
		},
	}
	setCmd.Flags().StringVar(&scope, "scope", "project", "Scope: project, shared, or global")
	setCmd.Flags().StringVar(&scopeID, "scope-id", "", "Explicit scope id (required for shared)")
	setCmd.Flags().StringVar(&knowledgeType, "type", "decision", "Knowledge type: decision, workflow, preference, incident")
	setCmd.Flags().StringVar(&title, "title", "", "Short title")
	setCmd.Flags().StringVar(&key, "key", "", "Stable memory key (defaults to a slug of title/content)")
	setCmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags (repeatable)")
	setCmd.Flags().StringVar(&path, "path", ".", "Workspace path for project scope resolution")

	var sScope, sScopeID, sType, sQuery, sPath string
	var sLimit int
	searchCmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search memory records in a scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			scopeType, resolvedID, err := resolveMemoryScope(cfg, sScope, sScopeID, sPath)
			if err != nil {
				return err
			}
			q := sQuery
			if q == "" && len(args) > 0 {
				q = strings.Join(args, " ")
			}
			res, err := memory.Search(cmd.Context(), store, memory.SearchInput{
				Query: q, ScopeType: scopeType, ScopeID: resolvedID, KnowledgeType: sType, Limit: sLimit,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), res, func(w io.Writer) error {
				return renderMemoryRecords(w, res.Matches)
			})
		},
	}
	searchCmd.Flags().StringVar(&sScope, "scope", "project", "Scope: project, shared, or global")
	searchCmd.Flags().StringVar(&sScopeID, "scope-id", "", "Explicit scope id (required for shared)")
	searchCmd.Flags().StringVar(&sType, "type", "", "Filter by knowledge type")
	searchCmd.Flags().StringVar(&sQuery, "query", "", "Search query (also accepted as positional arg)")
	searchCmd.Flags().IntVar(&sLimit, "limit", 20, "Maximum records")
	searchCmd.Flags().StringVar(&sPath, "path", ".", "Workspace path for project scope resolution")

	var rmReason, rmReplacement string
	rmCmd := &cobra.Command{
		Use:   "rm <node_id>",
		Short: "Deprecate a memory record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			rec, err := memory.Deprecate(cmd.Context(), store, memory.DeprecateInput{
				NodeID: args[0], ReplacementNodeID: rmReplacement, WriterID: writerID(), Reason: rmReason,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), rec, func(w io.Writer) error {
				fmt.Fprintf(w, "Deprecated %s (state %s)\n", rec.Node.ID, rec.LifecycleState)
				return nil
			})
		},
	}
	rmCmd.Flags().StringVar(&rmReason, "reason", "", "Why this memory is being deprecated")
	rmCmd.Flags().StringVar(&rmReplacement, "replacement", "", "Node id that replaces this memory")

	memCmd.AddCommand(setCmd, searchCmd, rmCmd)
	return memCmd
}

func newRulesCmd() *cobra.Command {
	rulesCmd := &cobra.Command{
		Use:   "rules",
		Short: "Manage agent rules scoped globally or to a codebase",
	}

	var scope, title, key, path string
	var tags []string
	addCmd := &cobra.Command{
		Use:   "add <rule text>",
		Short: "Add or update a rule (scope: global or project)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			scopeType, resolvedID, err := resolveMemoryScope(cfg, scope, "", path)
			if err != nil {
				return err
			}
			content := strings.Join(args, " ")
			memKey := key
			if memKey == "" {
				if title != "" {
					memKey = slugify(title)
				} else {
					memKey = slugify(content)
				}
			}
			out, err := memory.Put(cmd.Context(), store, cfg, memory.StoreInput{
				ScopeType: scopeType, ScopeID: resolvedID, KnowledgeType: "rule",
				Title: title, Content: content, Source: "cli", WriterID: writerID(), Tags: tags, MemoryKey: memKey,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), out, func(w io.Writer) error {
				fmt.Fprintf(w, "Added %s rule %q (id %s)\n", scopeType, out.Record.MemoryKey, out.Record.Node.ID)
				return nil
			})
		},
	}
	addCmd.Flags().StringVar(&scope, "scope", "project", "Rule scope: project (this codebase) or global")
	addCmd.Flags().StringVar(&title, "title", "", "Short rule title")
	addCmd.Flags().StringVar(&key, "key", "", "Stable rule key (defaults to a slug)")
	addCmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags (repeatable)")
	addCmd.Flags().StringVar(&path, "path", ".", "Workspace path for project scope resolution")

	var lScope, lQuery, lPath string
	var lLimit int
	var lAll bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List rules for a scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			var all []db.MemoryRecord
			scopes := []string{lScope}
			if lAll {
				scopes = []string{"global", "project"}
			}
			for _, sc := range scopes {
				scopeType, resolvedID, err := resolveMemoryScope(cfg, sc, "", lPath)
				if err != nil {
					return err
				}
				res, err := memory.Search(cmd.Context(), store, memory.SearchInput{
					Query: lQuery, ScopeType: scopeType, ScopeID: resolvedID, KnowledgeType: "rule", Limit: lLimit,
				})
				if err != nil {
					return err
				}
				all = append(all, res.Matches...)
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), memory.SearchOutput{Matches: all}, func(w io.Writer) error {
				return renderMemoryRecords(w, all)
			})
		},
	}
	listCmd.Flags().StringVar(&lScope, "scope", "project", "Rule scope: project or global")
	listCmd.Flags().BoolVar(&lAll, "all", false, "List both global and project rules")
	listCmd.Flags().StringVar(&lQuery, "query", "", "Filter rules by text")
	listCmd.Flags().IntVar(&lLimit, "limit", 50, "Maximum rules")
	listCmd.Flags().StringVar(&lPath, "path", ".", "Workspace path for project scope resolution")

	rmCmd := &cobra.Command{
		Use:   "rm <node_id>",
		Short: "Remove (deprecate) a rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			rec, err := memory.Deprecate(cmd.Context(), store, memory.DeprecateInput{NodeID: args[0], WriterID: writerID(), Reason: "removed via cli"})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), rec, func(w io.Writer) error {
				fmt.Fprintf(w, "Removed rule %s\n", rec.Node.ID)
				return nil
			})
		},
	}

	rulesCmd.AddCommand(addCmd, listCmd, rmCmd)
	return rulesCmd
}

func newCrawlCmd() *cobra.Command {
	var single bool
	cmd := &cobra.Command{
		Use:   "crawl <url>",
		Short: "Crawl a documentation site into the local graph store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Loading configuration...\n")
			cfg, err := config.LoadConfig()
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "Initializing local storage...\n")
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			mode := "full crawl"
			if single {
				mode = "single page"
			}
			fmt.Fprintf(out, "Creating %s crawler for %s...\n", mode, args[0])
			verbose.Printf("crawl url=%s single=%t", args[0], single)
			var docCrawler *crawler.DocumentationCrawler
			if single {
				docCrawler, err = crawler.NewSinglePageCrawler(store, cfg, args[0])
			} else {
				docCrawler, err = crawler.NewDocumentationCrawler(store, cfg, args[0])
			}
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "Crawling and indexing pages...\n")
			ctx := context.Background()
			if err := docCrawler.Run(ctx); err != nil {
				return err
			}

			stats := docCrawler.Stats()
			verbose.Printf("crawl complete pages=%d chunks=%d embeddings=%d", stats.PagesIndexed, stats.ChunksIndexed, stats.EmbeddingsCreated)
			fmt.Fprintf(out, "Crawl completed for %s: %d pages, %d chunks, %d embeddings\n", args[0], stats.PagesIndexed, stats.ChunksIndexed, stats.EmbeddingsCreated)
			return nil
		},
	}
	cmd.Flags().BoolVar(&single, "single", false, "Fetch and embed only the provided URL without following links")
	return cmd
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the MCP server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose.Printf("loading config for MCP start")
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			verbose.Printf("loaded config for MCP start: enabled=%t", cfg != nil)
			if cfg == nil {
				fmt.Fprintln(os.Stderr, "raph: no config found, semantic embeddings disabled; keyword search fallback remains available")
			}
			verbose.Printf("starting background sync worker")
			if started, err := syncer.Start(2 * time.Second); err != nil {
				verbose.Printf("sync worker start skipped: %v", err)
			} else if started {
				verbose.Printf("sync worker started for MCP session")
			}

			verbose.Printf("initializing local storage")
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			verbose.Printf("creating MCP server wrapper")
			server := serverpkg.NewMCPServerWrapper(store, cfg)
			verbose.Printf("starting MCP server over stdio")
			return server.Run(context.Background())
		},
	}
}

func newStudioCmd() *cobra.Command {
	var host string
	var port int

	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch the local graph explorer UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Loading configuration...\n")
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			if cfg == nil {
				fmt.Fprintln(os.Stderr, "raph: no config found, semantic embeddings disabled; keyword search fallback remains available")
			}

			fmt.Fprintf(out, "Initializing local storage...\n")
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			srv := studio.NewStudioServer(store, host, port)
			srv.SetConfig(cfg)
			if cwd, cwdErr := os.Getwd(); cwdErr == nil {
				srv.SetWorkspaceRoot(cwd)
				verbose.Printf("workspace root=%s", cwd)
			}
			verbose.Printf("launching studio on host=%s port=%d", host, port)
			fmt.Fprintf(out, "Starting raph Studio at http://%s:%d...\n", host, port)
			return srv.Start()
		},
	}

	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "Studio server host interface")
	cmd.Flags().IntVar(&port, "port", 4545, "Studio server port")
	return cmd
}

func newClearCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear all nodes and edges from local storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if !yes {
				return fmt.Errorf("refusing to clear graph without --yes")
			}

			fmt.Fprintf(out, "Initializing local storage...\n")
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			verbose.Printf("clearing all nodes, edges, memory records, and web corpora")
			if err := store.ClearAll(context.Background()); err != nil {
				return err
			}

			fmt.Fprintln(out, "Cleared local graph database")
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm destructive wipe of all graph data")
	return cmd
}

func newConfigCmd() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage ~/.raph configuration files",
	}

	var overwrite bool
	configCmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create ~/.raph/schema.json and ~/.raph/raph.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Writing default config files (overwrite=%t)...\n", overwrite)
			paths, err := config.WriteDefaultFiles(overwrite)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Wrote config assets under %s\n", paths.BaseDir)
			fmt.Fprintf(out, "Schema: %s\nConfig: %s\n", paths.SchemaFile, paths.ConfigFile)
			return nil
		},
	})
	configCmd.Commands()[0].Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing config files")

	configCmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print configuration and data paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose.Printf("resolving config paths")
			paths, err := config.GetConfigPaths()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "base=%s\nschema=%s\nconfig=%s\ndata=%s\n", paths.BaseDir, paths.SchemaFile, paths.ConfigFile, paths.DataDir)
			return nil
		},
	})

	configCmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Validate the current config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Loading and validating config...\n")
			cfg, err := config.LoadConfig()
			if err != nil {
				if errors.Is(err, config.ErrConfigNotFound) {
					return err
				}
				return err
			}
			verbose.Printf("config valid provider=%s model=%s embeddings=%t", cfg.Vector.CurrentProvider, cfg.Vector.Providers.OpenRouter.Model, cfg.HasEmbeddingProvider())
			fmt.Fprintf(out, "Config OK. Provider=%s Model=%s EmbeddingsEnabled=%t\n", cfg.Vector.CurrentProvider, cfg.Vector.Providers.OpenRouter.Model, cfg.HasEmbeddingProvider())
			return nil
		},
	})

	return configCmd
}

func newAgentsCmd() *cobra.Command {
	agentsCmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage coding-agent integrations",
	}

	var path string
	var dryRun bool
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP setup across supported coding agents",
	}
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Install or refresh project MCP config for supported coding agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Setting up MCP config for supported agents (dryRun=%t)...\n", dryRun)
			result, err := agentsetup.Setup(agentsetup.Options{Root: path, DryRun: dryRun})
			if err != nil {
				return err
			}
			verbose.Printf("setup complete root=%s agents=%d", result.Root, len(result.Outcomes))

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Preview only for %s\n", result.Root)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated project MCP config under %s\n", result.Root)
			}

			for _, outcome := range result.Outcomes {
				status := "missing"
				if outcome.Installed {
					status = "installed"
				}
				change := "unchanged"
				if outcome.Changed {
					change = "updated"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s) -> %s\n", outcome.Name, status, change, outcome.ConfigPath)
				if outcome.Message != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", outcome.Message)
				}
			}
			return nil
		},
	}
	setupCmd.Flags().StringVar(&path, "path", ".", "Project root to update")
	setupCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without writing files")

	mcpCmd.AddCommand(setupCmd)
	agentsCmd.AddCommand(mcpCmd)
	return agentsCmd
}

func newReleaseCmd() *cobra.Command {
	releaseCmd := &cobra.Command{
		Use:    "release",
		Short:  "Manage release integrity artifacts",
		Hidden: true,
	}

	var artifactPath string
	var signaturePath string
	var keyEnv string
	var passwordEnv string
	signCmd := &cobra.Command{
		Use:   "sign",
		Short: "Create a detached minisign signature for a release artifact",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if artifactPath == "" || signaturePath == "" {
				return fmt.Errorf("artifact and signature paths are required")
			}
			fmt.Fprintf(out, "Loading signing key from %s...\n", keyEnv)
			privateKeyText := []byte(os.Getenv(keyEnv))
			if len(privateKeyText) == 0 {
				return fmt.Errorf("%s is required", keyEnv)
			}
			password := os.Getenv(passwordEnv)
			privateKey, err := signing.ParsePrivateKey(privateKeyText, password)
			if err != nil {
				return err
			}
			verbose.Printf("reading artifact path=%s", artifactPath)
			artifact, err := os.ReadFile(artifactPath)
			if err != nil {
				return fmt.Errorf("read artifact: %w", err)
			}
			verbose.Printf("signing artifact size=%d", len(artifact))
			signature, err := signing.SignMessage(privateKey, artifact, "raph release artifact")
			if err != nil {
				return err
			}
			verbose.Printf("writing signature path=%s size=%d", signaturePath, len(signature))
			if err := os.WriteFile(signaturePath, signature, 0o644); err != nil {
				return fmt.Errorf("write signature: %w", err)
			}
			fmt.Fprintf(out, "Signed %s -> %s\n", artifactPath, signaturePath)
			return nil
		},
	}
	signCmd.Flags().StringVar(&artifactPath, "artifact", "", "Artifact file to sign")
	signCmd.Flags().StringVar(&signaturePath, "signature", "", "Destination signature file")
	signCmd.Flags().StringVar(&keyEnv, "key-env", signing.DefaultPrivateKeyEnv, "Environment variable containing the encrypted minisign private key")
	signCmd.Flags().StringVar(&passwordEnv, "password-env", signing.DefaultPasswordEnv, "Environment variable containing the minisign key password")
	_ = signCmd.MarkFlagRequired("artifact")
	_ = signCmd.MarkFlagRequired("signature")

	var verifyArtifactPath string
	var verifySignaturePath string
	verifyCmd := &cobra.Command{
		Use:   "verify-signature",
		Short: "Verify a detached minisign signature against the bundled public key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if verifyArtifactPath == "" || verifySignaturePath == "" {
				return fmt.Errorf("artifact and signature paths are required")
			}
			fmt.Fprintf(out, "Loading artifact and signature...\n")
			verbose.Printf("reading artifact path=%s", verifyArtifactPath)
			artifact, err := os.ReadFile(verifyArtifactPath)
			if err != nil {
				return fmt.Errorf("read artifact: %w", err)
			}
			verbose.Printf("reading signature path=%s", verifySignaturePath)
			signature, err := os.ReadFile(verifySignaturePath)
			if err != nil {
				return fmt.Errorf("read signature: %w", err)
			}
			verbose.Printf("verifying signature artifactSize=%d signatureSize=%d", len(artifact), len(signature))
			if err := signing.VerifyTrustedMessage(artifact, signature); err != nil {
				return err
			}
			fmt.Fprintf(out, "Verified %s with %s\n", verifyArtifactPath, verifySignaturePath)
			return nil
		},
	}
	verifyCmd.Flags().StringVar(&verifyArtifactPath, "artifact", "", "Artifact file to verify")
	verifyCmd.Flags().StringVar(&verifySignaturePath, "signature", "", "Detached signature file")
	_ = verifyCmd.MarkFlagRequired("artifact")
	_ = verifyCmd.MarkFlagRequired("signature")

	releaseCmd.AddCommand(signCmd)
	releaseCmd.AddCommand(verifyCmd)
	return releaseCmd
}
