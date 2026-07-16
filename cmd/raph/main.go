package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"raph/internal/agentsetup"
	"raph/internal/config"
	"raph/internal/crawler"
	"raph/internal/db"
	"raph/internal/exporter"
	"raph/internal/indexer"
	"raph/internal/knowledge"
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
	"golang.org/x/term"
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
			applyMemoryLimit() // soft heap ceiling; honors GOMEMLIMIT / RAPH_MEMORY_LIMIT
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
	rootCmd.AddCommand(newSCIPCmd())
	rootCmd.AddCommand(newMemCmd())
	rootCmd.AddCommand(newRulesCmd())
	rootCmd.AddCommand(newDocCmd())
	rootCmd.AddCommand(newExportCmd())
	rootCmd.AddCommand(newImportCmd())
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
				ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
			printSCIPGuidance(out, stats)
			return nil
		},
	}

	cmd.Flags().StringVar(&scanPath, "path", ".", "Workspace path to index")
	cmd.Flags().BoolVar(&noEmbeddings, "no-embeddings", false, "Skip remote embedding generation during indexing")
	return cmd
}

// printSCIPGuidance surfaces resolution-tier info after an index: which
// languages got compiler-grade resolution, and an actionable prompt for any
// language that could be upgraded by installing its indexer. Phrased so a human
// reads it as a suggestion and an agent can act on the install command itself.
func printSCIPGuidance(out io.Writer, stats indexer.Stats) {
	if len(stats.SCIPActive) > 0 {
		fmt.Fprintf(out, "Compiler-grade resolution active: %s\n", strings.Join(stats.SCIPActive, ", "))
	}
	if len(stats.SCIPSuggestions) == 0 {
		return
	}
	fmt.Fprintln(out, "\nUpgrade available — these languages used the bundled import-aware resolver.")
	fmt.Fprintln(out, "For go/types-level cross-file accuracy, install the matching indexer, then re-run `raph init`:")
	for _, s := range stats.SCIPSuggestions {
		fmt.Fprintf(out, "  %-12s raph code-intel install %s   (%s)\n", s.Language, s.Language, s.Install)
	}
	fmt.Fprintln(out, "Agents: ask the user before installing. If they decline, give them the command above to run themselves.")
}

func newSCIPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "code-intel",
		Aliases: []string{"scip"},
		Short:   "Show code-intelligence resolvers (compiler-grade cross-file accuracy) and install them",
		Long: "raph reaches go/types-grade cross-file reference accuracy for non-Go languages " +
			"by running an installed code-intelligence indexer (compiler-backed, SCIP) during a " +
			"full index. Languages without a tool fall back to the bundled import-aware resolver. " +
			"This lists each registered resolver and whether it is on PATH. " +
			"Use `raph code-intel install <language>` to install one (agents must ask the user first).",
		RunE: func(cmd *cobra.Command, args []string) error {
			status := indexer.SCIPStatus()
			return output.Print(cmd.OutOrStdout(), resolveFormat(), status, func(w io.Writer) error {
				var sb strings.Builder
				sb.WriteString("Code-intelligence resolvers (compiler-grade tier)\n")
				sb.WriteString("Go uses go/types natively. For others, install the tool below;\n")
				sb.WriteString("raph runs it automatically on the next full index. Disable with RAPH_NO_SCIP=1.\n\n")
				for _, s := range status {
					mark := "not installed"
					if s.Installed {
						mark = "installed (" + s.Path + ")"
					}
					fmt.Fprintf(&sb, "  %-12s %-16s %s\n", s.Language, s.Binary, mark)
					if !s.Installed {
						fmt.Fprintf(&sb, "               raph code-intel install %s   (%s)\n", s.Language, s.Install)
					}
				}
				_, err := io.WriteString(w, sb.String())
				return err
			})
		},
	}
	cmd.AddCommand(newSCIPInstallCmd())
	return cmd
}

func newSCIPInstallCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "install <language>",
		Short: "Install the compiler-grade code-intelligence resolver for a language",
		Long: "Installs the code-intelligence indexer for a language (e.g. `raph code-intel install python`), " +
			"then re-run `raph init` to upgrade resolution. AGENTS: ask the user for permission before " +
			"running this; if they decline, tell them to run it themselves. Use --dry-run to print " +
			"the command without executing it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			plan, ok := indexer.SCIPInstallPlanFor(args[0])
			if !ok {
				return fmt.Errorf("unknown language %q; registered resolvers: %s", args[0], strings.Join(indexer.SCIPLanguages(), ", "))
			}
			if plan.Installed {
				fmt.Fprintf(out, "%s resolver already installed (%s). Run `raph init` to use it.\n", plan.Language, plan.Binary)
				return nil
			}
			if len(plan.Argv) == 0 {
				return fmt.Errorf("no automated installer for %s — install manually: %s", plan.Language, plan.Hint)
			}
			fmt.Fprintf(out, "Install command: %s\n", strings.Join(plan.Argv, " "))
			if dryRun {
				return nil
			}
			// Preflight: the install relies on a package manager / runtime being
			// present (npm, pip, rustup, gem). Fail with a clear prerequisite
			// message instead of a cryptic exec error.
			if _, err := exec.LookPath(plan.Argv[0]); err != nil {
				return fmt.Errorf("%q is required to install the %s resolver but was not found on PATH; install %s first, or run manually: %s",
					plan.Argv[0], plan.Language, plan.Argv[0], plan.Hint)
			}
			run := exec.Command(plan.Argv[0], plan.Argv[1:]...)
			run.Stdout = out
			run.Stderr = cmd.ErrOrStderr()
			run.Stdin = os.Stdin
			if err := run.Run(); err != nil {
				return fmt.Errorf("install failed (%w); run it manually: %s", err, plan.Hint)
			}
			fmt.Fprintf(out, "Installed %s resolver. Re-run `raph init` to upgrade to compiler-grade resolution.\n", plan.Language)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the install command without running it")
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
		Short: "Search the indexed graph with familiar CLI ergonomics",
		Long: "Search indexed nodes without learning raph query syntax. Defaults to ranked " +
			"keyword search; use --literal for exact substrings, --regex for Go regexp patterns, or " +
			"--vector for semantic graph matches. Use --format json for the stable agent format.",
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
	cmd.Flags().BoolVar(&literal, "literal", false, "Exact substring match")
	cmd.Flags().BoolVar(&regex, "regex", false, "Treat the query as a Go regular expression")
	cmd.Flags().BoolVar(&vector, "vector", false, "Semantic graph search (requires a configured embedding provider)")
	// The three modes are mutually exclusive; reject conflicting input instead of
	// silently applying an undocumented precedence (which could quietly trigger
	// embedding work the user didn't ask for).
	cmd.MarkFlagsMutuallyExclusive("literal", "regex", "vector")
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
			// --limit is an overall maximum. When listing multiple scopes, spend a
			// shared budget across them so `--all --limit N` never returns > N.
			remaining := lLimit
			for _, sc := range scopes {
				if remaining <= 0 {
					break
				}
				scopeType, resolvedID, err := resolveMemoryScope(cfg, sc, "", lPath)
				if err != nil {
					return err
				}
				res, err := memory.Search(cmd.Context(), store, memory.SearchInput{
					Query: lQuery, ScopeType: scopeType, ScopeID: resolvedID, KnowledgeType: "rule", Limit: remaining,
				})
				if err != nil {
					return err
				}
				all = append(all, res.Matches...)
				remaining -= len(res.Matches)
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

// resolveDocWorkspace maps a doc scope to a workspace id (project workspace or
// the shared global-knowledge bucket).
func resolveDocWorkspace(store db.GraphStore, cfg *config.Config, scope, path string) (string, error) {
	switch strings.TrimSpace(scope) {
	case "", "project":
		return resolveWorkspaceID(store, cfg, path)
	case "global":
		return knowledge.GlobalWorkspace, nil
	default:
		return "", fmt.Errorf("unknown scope %q (use project or global)", scope)
	}
}

func renderDocList(w io.Writer, docs []db.Node) error {
	if len(docs) == 0 {
		_, err := io.WriteString(w, "No documents\n")
		return err
	}
	var sb strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&sb, "%s  [%s · %s]\n", d.Name, d.Prop("doc_type"), d.Prop("status"))
		fmt.Fprintf(&sb, "    id: %s\n", d.ID)
		if c := strings.TrimSpace(d.Content); c != "" {
			fmt.Fprintf(&sb, "    %s\n", query.NewExcerpt(c, 200))
		}
		sb.WriteString("\n")
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func newDocCmd() *cobra.Command {
	docCmd := &cobra.Command{
		Use:   "doc",
		Short: "Manage local documents and handoffs attached to the graph",
	}

	var scope, path, file, title, docType, source, key string
	var tags, links []string
	var noEmbed bool
	addCmd := &cobra.Command{
		Use:   "add [content]",
		Short: "Add a local document (from --file, stdin via '-', or inline text)",
		Long:  "Attach a document to local knowledge. Use --type to mark it as architecture, handoff, reference, or note so agents know how it serves the work.",
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

			content, err := readContent(cmd, file, args)
			if err != nil {
				return err
			}
			workspace, err := resolveDocWorkspace(store, cfg, scope, path)
			if err != nil {
				return err
			}
			doc, err := knowledge.Add(cmd.Context(), store, cfg, knowledge.AddInput{
				Workspace: workspace, Key: key, Title: title, Content: content,
				DocType: docType, Source: source, WriterID: writerID(), Tags: tags, Links: links, NoEmbed: noEmbed,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), doc, func(w io.Writer) error {
				fmt.Fprintf(w, "Added %s document %q (id %s, %d chunks)\n", doc.Node.Prop("doc_type"), doc.Node.Name, doc.Node.ID, doc.ChunkCount)
				return nil
			})
		},
	}
	addCmd.Flags().StringVar(&scope, "scope", "project", "Scope: project (this codebase) or global")
	addCmd.Flags().StringVar(&path, "path", ".", "Workspace path for project scope")
	addCmd.Flags().StringVar(&file, "file", "", "Read document content from a file")
	addCmd.Flags().StringVar(&title, "title", "", "Document title")
	addCmd.Flags().StringVar(&docType, "type", "note", "Document type: architecture, handoff, reference, note")
	addCmd.Flags().StringVar(&source, "source", "local", "Source: local, user, web")
	addCmd.Flags().StringVar(&key, "key", "", "Stable key (defaults to a slug of the title)")
	addCmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags (repeatable)")
	addCmd.Flags().StringSliceVar(&links, "link", nil, "Node ids to relate this document to (repeatable)")
	addCmd.Flags().BoolVar(&noEmbed, "no-embeddings", false, "Skip embedding generation")

	var lScope, lPath, lType, lStatus, lQuery string
	var lLimit int
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List documents in a scope",
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
			workspace, err := resolveDocWorkspace(store, cfg, lScope, lPath)
			if err != nil {
				return err
			}
			docs, err := knowledge.List(cmd.Context(), store, knowledge.ListFilter{
				Workspace: workspace, DocType: lType, Status: lStatus, Query: lQuery, Limit: lLimit,
			})
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), docs, func(w io.Writer) error {
				return renderDocList(w, docs)
			})
		},
	}
	listCmd.Flags().StringVar(&lScope, "scope", "project", "Scope: project or global")
	listCmd.Flags().StringVar(&lPath, "path", ".", "Workspace path for project scope")
	listCmd.Flags().StringVar(&lType, "type", "", "Filter by document type")
	listCmd.Flags().StringVar(&lStatus, "status", "", "Filter by status (fresh, stale, used)")
	listCmd.Flags().StringVar(&lQuery, "query", "", "Filter by text")
	listCmd.Flags().IntVar(&lLimit, "limit", 50, "Maximum documents")

	var noMark bool
	readCmd := &cobra.Command{
		Use:   "read <id>",
		Short: "Read a document with its chunks and related nodes (marks handoffs as used)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			doc, err := knowledge.Read(cmd.Context(), store, args[0], !noMark, writerID())
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), doc, func(w io.Writer) error {
				fmt.Fprintf(w, "%s  [%s · %s]\n\n%s\n", doc.Node.Name, doc.Node.Prop("doc_type"), doc.Node.Prop("status"), doc.Node.Content)
				if len(doc.Related) > 0 {
					fmt.Fprintf(w, "\nRelated:\n")
					for _, r := range doc.Related {
						fmt.Fprintf(w, "  - %s (%s) %s\n", r.Name, r.Type, r.ID)
					}
				}
				return nil
			})
		},
	}
	readCmd.Flags().BoolVar(&noMark, "no-mark", false, "Do not mark a handoff document as used")

	var rel string
	linkCmd := &cobra.Command{
		Use:   "link <from_id> <to_id>",
		Short: "Create a relation edge between two nodes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()
			if err := knowledge.Link(cmd.Context(), store, args[0], args[1], rel); err != nil {
				return err
			}
			linkResult := struct {
				From string `json:"from"`
				To   string `json:"to"`
				Rel  string `json:"rel"`
			}{From: args[0], To: args[1], Rel: rel}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), linkResult, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Linked %s -> %s (%s)\n", linkResult.From, linkResult.To, linkResult.Rel)
				return err
			})
		},
	}
	linkCmd.Flags().StringVar(&rel, "rel", knowledge.RelRelatesTo, "Relation type")

	docCmd.AddCommand(addCmd, listCmd, readCmd, linkCmd)
	return docCmd
}

// readContent resolves document content from --file, stdin ('-'), or args.
func readContent(cmd *cobra.Command, file string, args []string) (string, error) {
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		return string(data), nil
	}
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	}
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	return "", fmt.Errorf("provide content via --file, '-' for stdin, or inline text")
}

func newExportCmd() *cobra.Command {
	var docID, scope, path, format, out string
	var bundle, gist, public bool
	var repo, repoPath, s3Dest, r2Endpoint string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a document or knowledge bundle to a file and optionally publish it",
		Long: "Export local knowledge to a portable Markdown/JSON file, then optionally publish it " +
			"to a GitHub gist, a repo, S3, or Cloudflare R2 so it transfers between machines.",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			cfg, _ := config.LoadConfigIfPresent() // only used for project-scope resolution; optional

			fmtVal := exporter.Format(format)
			if fmtVal != exporter.FormatJSON {
				fmtVal = exporter.FormatMarkdown
			}

			var artifact exporter.Artifact
			switch {
			case bundle:
				scopeSel := scope
				if !cmd.Flags().Changed("scope") {
					scopeSel = "portable" // brain bundle defaults to portable scopes
				}
				scopeTypes, scErr := brainScopeTypes(scopeSel)
				if scErr != nil {
					return scErr
				}
				brainScope := exporter.BrainScope{ScopeTypes: scopeTypes}
				// A project-only bundle must be pinned to the current repo, or it
				// would sweep in every indexed repo's project-scoped memory and
				// handoffs — a data leak when publishing to gist/repo/S3.
				if len(scopeTypes) == 1 && scopeTypes[0] == "project" {
					_, pid, e := resolveMemoryScope(cfg, "project", "", path)
					if e != nil {
						return fmt.Errorf("resolve project scope for export: %w", e)
					}
					brainScope.ProjectScopeID = pid
					if ws, wErr := resolveWorkspaceID(store, cfg, path); wErr == nil {
						brainScope.ProjectWorkspace = ws
					}
				}
				artifact, err = exporter.Brain(cmd.Context(), store, brainScope, fmtVal)
			case strings.TrimSpace(docID) != "":
				artifact, err = exporter.Document(cmd.Context(), store, docID, fmtVal)
			default:
				return fmt.Errorf("provide --doc <id> or --bundle")
			}
			if err != nil {
				return err
			}

			target, err := exporter.Write(artifact, out)
			if err != nil {
				return err
			}

			result := map[string]any{"file": target, "bytes": artifact.Bytes}
			if gist {
				url, gistErr := exporter.UploadGist(cmd.Context(), target, public, "raph knowledge export")
				if gistErr != nil {
					return gistErr
				}
				result["gist_url"] = url
			}
			if strings.TrimSpace(repo) != "" {
				rp := repoPath
				if rp == "" {
					rp = filepath.Base(target)
				}
				if err := exporter.UploadRepoFile(cmd.Context(), repo, rp, target, ""); err != nil {
					return err
				}
				result["repo"] = repo + "/" + rp
			}
			if strings.TrimSpace(s3Dest) != "" {
				if err := exporter.UploadS3(cmd.Context(), target, s3Dest, r2Endpoint); err != nil {
					return err
				}
				result["s3"] = s3Dest
			}

			return output.Print(cmd.OutOrStdout(), resolveFormat(), result, func(w io.Writer) error {
				fmt.Fprintf(w, "Exported %d bytes to %s\n", artifact.Bytes, target)
				if v, ok := result["gist_url"]; ok {
					fmt.Fprintf(w, "Gist: %v\n", v)
				}
				if v, ok := result["repo"]; ok {
					fmt.Fprintf(w, "Repo: %v\n", v)
				}
				if v, ok := result["s3"]; ok {
					fmt.Fprintf(w, "Uploaded to %v\n", v)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&docID, "doc", "", "Document node id to export")
	cmd.Flags().BoolVar(&bundle, "bundle", false, "Export all documents in the scope as one bundle")
	cmd.Flags().StringVar(&scope, "scope", "project", "Scope for --bundle: project or global")
	cmd.Flags().StringVar(&path, "path", ".", "Workspace path for project scope")
	cmd.Flags().StringVar(&format, "out-format", "json", "Export content format: json (round-trippable via `raph import`) or md (human-readable only)")
	cmd.Flags().StringVar(&out, "out", "", "Output file or directory (default: auto-named in cwd)")
	cmd.Flags().BoolVar(&gist, "gist", false, "Publish the export as a GitHub gist")
	cmd.Flags().BoolVar(&public, "public", false, "Make the gist public")
	cmd.Flags().StringVar(&repo, "repo", "", "Publish to a GitHub repo (owner/name)")
	cmd.Flags().StringVar(&repoPath, "repo-path", "", "Destination path within the repo")
	cmd.Flags().StringVar(&s3Dest, "s3", "", "Upload to an S3/R2 destination (s3://bucket/key)")
	cmd.Flags().StringVar(&r2Endpoint, "r2-endpoint", "", "Custom S3 endpoint URL (e.g. Cloudflare R2)")
	return cmd
}

func newImportCmd() *cobra.Command {
	var noEmbed bool

	cmd := &cobra.Command{
		Use:   "import <file|url|->",
		Short: "Import a brain export (memory, rules, handoffs) into the local graph",
		Long: "Load a raph brain export (from `raph export --bundle`) into the local graph. The source " +
			"can be a local file, an http(s) URL (e.g. a raw JSON link), or `-` to read stdin. Memory and " +
			"rules are restored under their original scope; handoffs are reconstructed (chunks and " +
			"embeddings regenerate locally). Re-importing the same export updates records in place.",
		Args: cobra.ExactArgs(1),
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

			data, err := fetchImportSource(cmd.Context(), args[0], cmd.InOrStdin())
			if err != nil {
				return err
			}
			res, err := exporter.Import(cmd.Context(), store, cfg, data, noEmbed)
			if err != nil {
				return err
			}
			return output.Print(cmd.OutOrStdout(), resolveFormat(), res, func(w io.Writer) error {
				fmt.Fprintf(w, "Imported %d memory/rule record(s), %d handoff(s)", res.Memory, res.Handoffs)
				if res.Skipped > 0 {
					fmt.Fprintf(w, " (%d skipped)", res.Skipped)
				}
				fmt.Fprintln(w)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&noEmbed, "no-embed", false, "Skip regenerating embeddings on import")
	return cmd
}

// brainScopeTypes maps an export --scope selector to the memory scope types a
// brain bundle should gather.
func brainScopeTypes(scope string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "portable":
		return []string{"global", "shared"}, nil
	case "all":
		return []string{"global", "shared", "project"}, nil
	case "global":
		return []string{"global"}, nil
	case "shared":
		return []string{"shared"}, nil
	case "project":
		return []string{"project"}, nil
	default:
		return nil, fmt.Errorf("unknown scope %q (use portable, all, global, shared, or project)", scope)
	}
}

// fetchImportSource resolves an import argument to raw bytes: `-` reads stdin,
// an existing path reads the file, and an http(s) URL is fetched directly.
func fetchImportSource(ctx context.Context, source string, stdin io.Reader) ([]byte, error) {
	source = strings.TrimSpace(source)
	switch {
	case source == "-":
		return io.ReadAll(stdin)
	case strings.HasPrefix(source, "http://"), strings.HasPrefix(source, "https://"):
		if strings.HasPrefix(source, "http://") {
			fmt.Fprintf(os.Stderr, "raph: warning: importing over plain http is susceptible to tampering; prefer https for %s\n", source)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", source, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: status %s", source, resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MiB ceiling
	default:
		if _, statErr := os.Stat(source); statErr == nil {
			return os.ReadFile(source)
		}
		return nil, fmt.Errorf("%q is not a readable file, http(s) URL, or `-` (stdin)", source)
	}
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

			// Storage opens lazily on the first tool call. Opening brain.db
			// eagerly can wait multiple seconds on the write lock while the
			// sync worker is mid-index, and MCP clients (opencode gives local
			// servers 5s by default) mark the server failed if the initialize
			// handshake doesn't complete in time.
			verbose.Printf("deferring local storage open until first tool call")
			store := db.NewLazyStore(func() (db.GraphStore, error) {
				verbose.Printf("initializing local storage")
				s, err := db.InitStorage()
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			defer store.Close()

			verbose.Printf("creating MCP server wrapper")
			server := serverpkg.NewMCPServerWrapper(store, cfg)
			verbose.Printf("starting MCP server over stdio")
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return server.Run(ctx)
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
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return srv.Start(ctx)
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
	var scopeFlag string
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP setup across supported coding agents",
	}
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Install or refresh MCP config for supported coding agents",
		Long: "Install or refresh the raph MCP entry for supported coding agents.\n\n" +
			"By default entries go to each agent's global (user-level) config so every\n" +
			"project picks them up. Use --scope local to write project files under --path\n" +
			"instead. Without --scope, an interactive terminal is prompted for the choice.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			scope, err := resolveSetupScope(cmd, scopeFlag, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Setting up %s MCP config for supported agents (dryRun=%t)...\n", scope, dryRun)
			result, err := agentsetup.Setup(agentsetup.Options{Root: path, DryRun: dryRun, Scope: scope})
			if err != nil {
				return err
			}
			verbose.Printf("setup complete root=%s scope=%s agents=%d", result.Root, result.Scope, len(result.Outcomes))

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Preview only (%s scope)\n", result.Scope)
			} else if result.Scope == agentsetup.ScopeLocal {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated project MCP config under %s\n", result.Root)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated global (user-level) MCP config\n")
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
				if outcome.PluginPath != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  plugin -> %s\n", outcome.PluginPath)
				}
				if outcome.Message != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", outcome.Message)
				}
			}
			return nil
		},
	}
	setupCmd.Flags().StringVar(&path, "path", ".", "Project root to update (used by --scope local)")
	setupCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without writing files")
	setupCmd.Flags().StringVar(&scopeFlag, "scope", "", "Where to install MCP entries: global (user-level config, default) or local (project files)")

	mcpCmd.AddCommand(setupCmd)
	agentsCmd.AddCommand(mcpCmd)
	return agentsCmd
}

// resolveSetupScope decides where agents mcp setup writes. An explicit
// --scope wins; otherwise an interactive terminal is asked, defaulting to
// global, and non-interactive runs (pipes, scripts, agents) use global.
func resolveSetupScope(cmd *cobra.Command, scopeFlag string, path string) (string, error) {
	if strings.TrimSpace(scopeFlag) != "" {
		return agentsetup.ParseScope(scopeFlag)
	}
	if !stdinIsTerminal() {
		return agentsetup.ScopeGlobal, nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Where should the raph MCP entries be installed?")
	fmt.Fprintln(out, "  [G] global — user-level agent configs, applies to every project (default)")
	fmt.Fprintf(out, "  [l] local  — project files under %s\n", path)
	fmt.Fprint(out, "Scope [G/l]: ")

	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		// No answer available (e.g. closed stdin); fall back to the default.
		return agentsetup.ScopeGlobal, nil
	}
	return agentsetup.ParseScope(line)
}

func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
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
