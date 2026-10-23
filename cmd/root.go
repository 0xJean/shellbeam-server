package cmd

import (
	"fmt"
	"os"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/utils"

	"github.com/spf13/cobra"
)

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

var RootCmd = &cobra.Command{
	Use:     "shellbeam-server",
	Short:   "ShellBeam Server is a self-hosted monitoring backend",
	Long:    "ShellBeam Server is a self-hosted monitoring backend compatible with Komari APIs and agents.",
	Version: utils.ServerVersion,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.SetArgs([]string{"server"})
		cmd.Execute()
	},
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseType, "db-type", "t", "sqlite", "Database type (sqlite)")
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseFile, "database", "d", "./data/komari.db", "SQLite database file path")
}
