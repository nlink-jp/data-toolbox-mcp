package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/nlink-jp/data-toolbox-mcp/internal/config"
	"github.com/nlink-jp/data-toolbox-mcp/internal/logging"
	"github.com/nlink-jp/data-toolbox-mcp/internal/mcpserver"
	"github.com/nlink-jp/data-toolbox-mcp/internal/tools"
	"github.com/nlink-jp/data-toolbox-mcp/internal/transport"
	"github.com/nlink-jp/data-toolbox-mcp/internal/workspace"
	"github.com/spf13/cobra"
)

// configPath is bound to a persistent flag on rootCmd so both
// `data-toolbox-mcp serve --config=…` and the bare `data-toolbox-mcp --config=…`
// pick it up.
var configPath string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP stdio server",
	Long:  "Read JSON-RPC messages from stdin and serve MCP tool calls. This is the default when no subcommand is given.",
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := resolveConfig(configPath)
	if err != nil {
		return err
	}

	logger, logFile, err := logging.Setup(cfg.Server.LogLevel, cfg.Server.LogFile)
	if err != nil {
		return err
	}
	if logFile != nil {
		defer logFile.Close()
	}

	tr := transport.NewStdioTransport(os.Stdin, os.Stdout)
	srv := mcpserver.New("data-toolbox-mcp", Version, tr, logger)

	pc := workspace.NewPodmanClient()
	mgr := workspace.NewManager(cfg, pc)
	tools.Register(srv, mgr, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	defer func() {
		if cfg.Container.StopOnExit {
			// Best-effort cleanup; we don't fail shutdown on Podman errors.
			_ = mgr.Cleanup(context.Background())
		}
	}()

	if err := srv.Serve(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}

// resolveConfig loads explicit path if given, otherwise searches standard
// locations. Returns config.Default() when no file is found.
func resolveConfig(explicit string) (*config.Config, error) {
	if explicit != "" {
		return config.Load(explicit)
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, ".config", "data-toolbox-mcp", "config.toml"),
		"config.toml",
	} {
		if _, err := os.Stat(c); err == nil {
			return config.Load(c)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat %s: %w", c, err)
		}
	}
	return config.Default(), nil
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
