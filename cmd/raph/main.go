package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"raph/internal/config"
	"raph/internal/crawler"
	"raph/internal/db"
	"raph/internal/indexer"
	serverpkg "raph/internal/mcp"
	"raph/internal/studio"
	"raph/internal/syncer"
	"raph/internal/updater"
	"raph/internal/version"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "raph",
		Short:         "raph is a local-first graph-vector brain for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Full(),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if cmd.Name() == "update" || version.Version == "dev" || !updater.ShouldAutoCheck() {
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

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newCrawlCmd())
	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newStudioCmd())
	rootCmd.AddCommand(newSyncCmd())
	rootCmd.AddCommand(newClearCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newUpdateCmd())
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and build date",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
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
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			result, err := updater.Update(ctx, version.Version)
			if err != nil {
				return err
			}
			if result.Updated {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated raph from %s to %s\n", result.Current, result.Latest)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "raph %s is already current\n", result.Current)
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
			if worker {
				ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
				defer cancel()
				return syncer.RunWorker(ctx, interval)
			}
			if status {
				running, pid, err := syncer.Status()
				if err != nil {
					return err
				}
				repositories, err := syncer.List()
				if err != nil {
					return err
				}
				if running {
					fmt.Fprintf(cmd.OutOrStdout(), "Sync worker running (pid %d)\n", pid)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "Sync worker stopped")
				}
				for _, repo := range repositories {
					fmt.Fprintf(cmd.OutOrStdout(), "%s (embeddings=%t)\n", repo.Path, !repo.NoEmbeddings)
				}
				return nil
			}
			if stop {
				stopped, err := syncer.Stop()
				if err != nil {
					return err
				}
				if stopped {
					fmt.Fprintln(cmd.OutOrStdout(), "Stopped sync worker")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "Sync worker is not running")
				}
				return nil
			}
			if remove {
				if err := syncer.Remove(context.Background(), scanPath, !keepData); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Stopped syncing %s (graph data removed=%t)\n", scanPath, !keepData)
				return nil
			}

			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			idx, err := indexer.New(store, cfg, scanPath, noEmbeddings)
			if err != nil {
				_ = store.Close()
				return err
			}
			stats, err := idx.Run(context.Background())
			_ = store.Close()
			if err != nil {
				return err
			}
			repo, err := syncer.Register(scanPath, noEmbeddings)
			if err != nil {
				return err
			}
			started, err := syncer.Start(interval)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sync enabled for %s: %d files, %d nodes, %d edges\n", repo.Path, stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved)
			if started {
				fmt.Fprintln(cmd.OutOrStdout(), "Background sync worker started")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Background sync worker already running")
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
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}

			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			idx, err := indexer.New(store, cfg, scanPath, noEmbeddings)
			if err != nil {
				return err
			}

			stats, err := idx.Run(context.Background())
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Indexed %d files, %d nodes, %d edges, %d embeddings\n", stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved, stats.EmbeddingsCreated)
			if !noEmbeddings && (cfg == nil || !cfg.HasEmbeddingProvider()) {
				fmt.Fprintln(cmd.OutOrStdout(), "No resolved embedding provider key found; graph was indexed without embeddings. Run `raph config init` and set OPENROUTER_API_KEY to enable semantic search.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&scanPath, "path", ".", "Workspace path to index")
	cmd.Flags().BoolVar(&noEmbeddings, "no-embeddings", false, "Skip remote embedding generation during indexing")
	return cmd
}

func newCrawlCmd() *cobra.Command {
	var single bool
	cmd := &cobra.Command{
		Use:   "crawl <url>",
		Short: "Crawl a documentation site into the local graph store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig()
			if err != nil {
				return err
			}

			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			var docCrawler *crawler.DocumentationCrawler
			if single {
				docCrawler, err = crawler.NewSinglePageCrawler(store, cfg, args[0])
			} else {
				docCrawler, err = crawler.NewDocumentationCrawler(store, cfg, args[0])
			}
			if err != nil {
				return err
			}

			ctx := context.Background()
			if err := docCrawler.Run(ctx); err != nil {
				return err
			}

			stats := docCrawler.Stats()
			fmt.Fprintf(cmd.OutOrStdout(), "Crawl completed for %s: %d pages, %d chunks, %d embeddings\n", args[0], stats.PagesIndexed, stats.ChunksIndexed, stats.EmbeddingsCreated)
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
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			if cfg == nil {
				fmt.Fprintln(os.Stderr, "raph: no config found, semantic embeddings disabled; keyword search fallback remains available")
			}

			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			server := serverpkg.NewMCPServerWrapper(store, cfg)
			return server.Run(context.Background())
		},
	}
}

func newStudioCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch the local graph explorer UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigIfPresent()
			if err != nil {
				return err
			}
			if cfg == nil {
				fmt.Fprintln(os.Stderr, "raph: no config found, semantic embeddings disabled; keyword search fallback remains available")
			}

			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			srv := studio.NewStudioServer(store, port)
			srv.SetConfig(cfg)
			return srv.Start()
		},
	}

	cmd.Flags().IntVar(&port, "port", 4545, "Studio server port")
	return cmd
}

func newClearCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear all nodes and edges from local storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to clear graph without --yes")
			}

			store, err := db.InitStorage()
			if err != nil {
				return err
			}
			defer store.Close()

			if err := store.ClearAll(context.Background()); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Cleared local graph database")
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
			paths, err := config.WriteDefaultFiles(overwrite)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote config assets under %s\n", paths.BaseDir)
			fmt.Fprintf(cmd.OutOrStdout(), "Schema: %s\nConfig: %s\n", paths.SchemaFile, paths.ConfigFile)
			return nil
		},
	})
	configCmd.Commands()[0].Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing config files")

	configCmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print configuration and data paths",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			cfg, err := config.LoadConfig()
			if err != nil {
				if errors.Is(err, config.ErrConfigNotFound) {
					return err
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Config OK. Provider=%s Model=%s EmbeddingsEnabled=%t\n", cfg.Vector.CurrentProvider, cfg.Vector.Providers.OpenRouter.Model, cfg.HasEmbeddingProvider())
			return nil
		},
	})

	return configCmd
}
