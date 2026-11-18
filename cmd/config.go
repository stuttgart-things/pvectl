package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

var configFlags struct {
	vmid     int
	noChecks bool
}

// ConfigResult is `config -o json`. The description is never part of it.
type ConfigResult struct {
	VMID     int         `json:"vmid"`
	Name     string      `json:"name"`
	Node     string      `json:"node"`
	Template bool        `json:"template"`
	Config   pve.Config  `json:"config"`
	Disks    []pve.Disk  `json:"disks"`
	Checks   []pve.Check `json:"checks,omitempty"`
	ChecksOK *bool       `json:"checks_ok,omitempty"`
}

var configCmd = &cobra.Command{
	Use:     "config [vmid]",
	GroupID: groupRead,
	Short:   "Show a VM/template config and check cpu: host, guest agent and cloud-init drive",
	Long: `Show the config of a VM or template (the node is looked up) and run the
template sanity checks:

  cpu               host (EL10 needs x86-64-v3; #4650)
  agent             enabled, or Proxmox never learns the guest IP
  cloud-init drive  present, or clones keep the template hostname

A failed check exits with 4 (--no-checks skips them). The description
(notes) is never shown, and there is no flag to show it: in the LabUL
templates it holds plaintext passwords. cipassword shows as "(set)".`,
	Example: `  # human; without an ID it asks (template/VM list with filter)
  pvectl config 280
  pvectl config

  # programmatic
  pvectl config 280 -o json | jq '.checks_ok'
  pvectl config --vmid 279 --non-interactive || echo "exit $?"   # 4 = check failed`,
	Args: maxArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := newClient()
		if err != nil {
			return err
		}
		r, err := resolveGuest(ctx, c, args, configFlags.vmid, "--vmid", pve.KindAll, "Which VM or template?")
		if err != nil {
			return err
		}
		cfg, err := c.Config(ctx, r.Node, r.VMID)
		if err != nil {
			return err
		}
		res := ConfigResult{VMID: r.VMID, Name: r.Name, Node: r.Node, Template: cfg.IsTemplate(), Config: cfg, Disks: cfg.Disks()}
		if res.Disks == nil {
			res.Disks = []pve.Disk{}
		}
		if !configFlags.noChecks {
			res.Checks = pve.CheckConfig(cfg)
			ok := pve.ChecksOK(res.Checks)
			res.ChecksOK = &ok
		}
		if jsonOut() {
			if err := printJSON(res); err != nil {
				return err
			}
		} else {
			printConfig(res)
		}
		if res.ChecksOK != nil && !*res.ChecksOK {
			return silentErr(ExitCheckFailed, fmt.Errorf("VMID %d (%s): %w", r.VMID, r.Name, errChecks))
		}
		return nil
	},
}

// resolveGuest finds the guest named by a positional ID or flag; without
// one it asks (interactive) or fails with the flag (programmatic).
func resolveGuest(ctx context.Context, c *pve.Client, args []string, flagID int, flag string, kind pve.Kind, title string) (pve.Resource, error) {
	id := flagID
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return pve.Resource{}, &cliError{code: ExitUsage, flag: flag, err: fmt.Errorf("VMID must be a number, not %q", args[0])}
		}
		if flagID != 0 && flagID != n {
			return pve.Resource{}, usageErr("VMID given twice (%d and %s %d)", n, flag, flagID)
		}
		id = n
	}
	if id == 0 {
		if !interactive() {
			return pve.Resource{}, missingFlag(flag, "VMID")
		}
		return pickGuest(ctx, c, kind, title)
	}
	return c.FindVM(ctx, id)
}

func printConfig(r ConfigResult) {
	w := os.Stdout
	if fancy() {
		_, _ = fmt.Fprintln(w, configCard(r))
		return
	}
	_, _ = fmt.Fprintln(w, guestTitle(r.VMID, r.Name, r.Node, r.Template))
	printSection(w, "config")
	for _, k := range r.Config.Keys() {
		_, _ = fmt.Fprintf(w, "%s %s\n", keyStyle.Render(fmt.Sprintf("%-12s", k+":")), r.Config[k])
	}
	if len(r.Disks) > 0 {
		printSection(w, "disks")
		for _, d := range r.Disks {
			_, _ = fmt.Fprintf(w, "%-10s %-14s %s\n", d.Key, d.Storage, d.Volume)
		}
	}
	if r.Checks != nil {
		printSection(w, "checks")
		for _, ch := range r.Checks {
			_, _ = fmt.Fprintf(w, "%s %-17s %s\n", checkMark(ch), ch.Name, ch.Detail)
		}
	}
}

// configCard renders config, disks and checks side by side where the
// terminal is wide enough, else stacked. r.Config never holds the
// description (see pve.SafeConfig).
func configCard(r ConfigResult) string {
	var cfg []string
	for _, k := range r.Config.Keys() {
		cfg = append(cfg, keyStyle.Render(fmt.Sprintf("%-12s", k))+" "+r.Config[k])
	}
	leftContent := heading("config") + "\n\n" + strings.Join(cfg, "\n")
	left := cardStyle.Render(leftContent)

	var right, rightContent []string
	if r.Checks != nil {
		var lines []string
		for _, ch := range r.Checks {
			lines = append(lines, fmt.Sprintf("%s %s  %s", checkMark(ch), textStyle.Bold(true).Render(fmt.Sprintf("%-17s", ch.Name)), dimStyle.Render(ch.Detail)))
		}
		rightContent = append(rightContent, heading("checks")+"\n\n"+strings.Join(lines, "\n"))
		right = append(right, cardStyle.Render(rightContent[len(rightContent)-1]))
	}
	if len(r.Disks) > 0 {
		var lines []string
		for _, d := range r.Disks {
			lines = append(lines, fmt.Sprintf("%s %s %s", keyStyle.Render(fmt.Sprintf("%-8s", d.Key)), lipgloss.NewStyle().Foreground(colPink).Render(fmt.Sprintf("%-14s", d.Storage)), dimStyle.Render(d.Volume)))
		}
		rightContent = append(rightContent, heading("disks")+"\n\n"+strings.Join(lines, "\n"))
		right = append(right, cardStyle.Render(rightContent[len(rightContent)-1]))
	}
	body := left
	if len(right) > 0 {
		col := lipgloss.JoinVertical(lipgloss.Left, right...)
		if lipgloss.Width(left)+lipgloss.Width(col)+1 <= termWidth() {
			body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", col)
		} else {
			body = cards(cardStyle, append([]string{leftContent}, rightContent...)...)
		}
	}
	return "\n" + guestTitle(r.VMID, r.Name, r.Node, r.Template) + "\n" + body
}

func init() {
	configCmd.Flags().IntVar(&configFlags.vmid, "vmid", 0, "VMID (same as the argument)")
	configCmd.Flags().BoolVar(&configFlags.noChecks, "no-checks", false, "skip the sanity checks")
	rootCmd.AddCommand(configCmd)
}
