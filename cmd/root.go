package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "data-toolbox-mcp",
	Short: "DuckDB + Python sandbox MCP server",
	Long: `data-toolbox-mcp exposes DuckDB analysis and containerized Python execution as an MCP server.

When invoked with no subcommand, behaves like ` + "`data-toolbox-mcp serve`" + ` and reads JSON-RPC messages from stdin.`,
	// Don't dump the usage help on RunE errors; cobra still prints "Error: ..." to stderr.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServe(cmd, args)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "",
		"Path to config.toml (default: search ~/.config/data-toolbox-mcp/config.toml then ./config.toml)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// cobra has already printed "Error: ..." to stderr.
		_ = err
		os.Exit(1)
	}
}
