package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// global flags
var opts struct {
	output         string
	nonInteractive bool
	insecure       bool
}

const (
	groupRead  = "read"
	groupWrite = "write"
)

var rootCmd = &cobra.Command{
	Use:   "pvectl",
	Short: "LabUL Proxmox: find VMs and templates, check configs, follow tasks, migrate, promote templates",
	Long: `pvectl covers the recurring Proxmox steps of a LabUL template round:
check config -> migrate ul-pve01 -> ul-pve11 -> FS9100 copy -> repin.

Credentials come from the environment and are never printed:

  sops exec-env ~/.secrets.d/labul/proxmox.env 'pvectl vms ubuntu26'

  PVE_API_URL           https://<host>:8006/api2/json
  PVE_API_USER          user@realm
  PVE_API_PASSWORD      password
  PVE_API_TOKEN_ID      or an API token (CI, Dagger): user@realm!tokenname
  PVE_API_TOKEN_SECRET  its secret; set, the token is used instead
  PVE_CA_CERT           optional: the cluster CA (/etc/pve/pve-root-ca.pem), path or PEM

TLS is always verified: by the system store or PVE_CA_CERT, else by a
certificate pinned once with 'pvectl tls trust' (like SSH known_hosts; a
first interactive run offers it). --insecure skips the check: last resort.

Two modes, same logic:
  interactive      a TTY, no -o json, no --non-interactive: missing arguments are
                   asked for, writes show a summary and ask for confirmation
  programmatic     everything via flags; never prompts; -o json prints one JSON
                   document on stdout (errors too), logs and progress on stderr

Exit codes: 0 ok, 1 error, 2 usage, 3 not found, 4 check failed,
5 Proxmox task failed, 130 aborted.

VM descriptions (notes) are never printed: in LabUL they hold passwords.`,
	SilenceErrors: true,
	SilenceUsage:  true,
	// pvectl alone: the start menu on a terminal, the help otherwise
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usageErr("unknown command %q for \"pvectl\"", args[0])
		}
		if !interactive() {
			return cmd.Help()
		}
		return runMenu(cmd)
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		switch opts.output {
		case "text", "json":
		default:
			return &cliError{code: ExitUsage, flag: "--output", err: fmt.Errorf("--output must be text or json, not %q", opts.output)}
		}
		return nil
	},
}

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: groupRead, Title: "Read (safe to run any time):"},
		&cobra.Group{ID: groupWrite, Title: "Write (summary + confirmation, --dry-run, --yes):"},
	)
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&opts.output, "output", "o", "text", "output format: text or json")
	pf.BoolVar(&opts.nonInteractive, "non-interactive", false, "never prompt; fail with the missing flag instead")
	pf.BoolVar(&opts.insecure, "insecure", false, "skip TLS verification for this run (last resort; see 'pvectl tls trust')")
	rootCmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &cliError{code: ExitUsage, err: err}
	})
}

// Execute runs the CLI and exits with the documented exit code.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := rootCmd.ExecuteContext(ctx)
	if err != nil && offerTrust(ctx, err) {
		err = rootCmd.ExecuteContext(ctx)
	}
	stop()
	os.Exit(reportError(err))
}

// interactive decides the mode: a TTY on stdin and stdout, no -o json, no
// --non-interactive.
func interactive() bool {
	if opts.nonInteractive || opts.output == "json" {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

func jsonOut() bool { return opts.output == "json" }

// stderrTTY reports whether progress on stderr can use colors and redraws.
func stderrTTY() bool { return isatty.IsTerminal(os.Stderr.Fd()) }

// newClient builds the API client from the environment.
func newClient() (*pve.Client, error) {
	o, err := pve.OptionsFromEnv()
	if err != nil {
		return nil, err
	}
	o.Insecure = opts.insecure
	return pve.New(o)
}

// printJSON writes v as the one JSON document of the run.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// logf writes a progress/log line to stderr (never stdout).
func logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	if stderrTTY() {
		line = dimStyle.Render(line)
	}
	_, _ = fmt.Fprintln(os.Stderr, line)
}
