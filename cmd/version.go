package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/internal/buildinfo"
)

// Set via -ldflags -X github.com/stuttgart-things/pvectl/cmd.<name>=… (see
// Taskfile.yaml build-local and .goreleaser.yaml). Empty values fall back to
// the build info the Go toolchain embeds.
var (
	version   = ""
	commit    = ""
	buildDate = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Args:  maxArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		info := buildinfo.Current(version, commit, buildDate)
		if jsonOut() {
			return printJSON(map[string]string{"version": info.Version, "commit": info.Commit, "date": info.Date})
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "pvectl "+info.String())
		return err
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
