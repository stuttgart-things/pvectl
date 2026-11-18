package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

var vmsFlags struct {
	templates bool
	all       bool
	details   bool
	days      int
}

// VMRow is one entry of `vms -o json`.
type VMRow struct {
	pve.Resource
	IPs      []string   `json:"ips,omitempty"`
	IPError  string     `json:"ip_error,omitempty"`
	Tasks    []pve.Task `json:"tasks,omitempty"`
	NumTasks int        `json:"num_tasks,omitempty"`
}

var vmsCmd = &cobra.Command{
	Use:     "vms [filter]",
	GroupID: groupRead,
	Short:   "List VMs (or templates) by name substring or VMID, with node, status, guest IP and recent tasks",
	Long: `List guests whose name contains <filter> or whose VMID equals it.

Default: VMs only (no templates). --templates lists only templates, --all both.
With a filter (or --details) each VM also shows its guest-agent IPv4 and the
qm*/ha* tasks of the last --days days, like pve.py vms. Without a filter the
list stays a plain table. Descriptions are never shown.`,
	Example: `  # human
  sops exec-env ~/.secrets.d/labul/proxmox.env 'pvectl vms platform-sthings'
  pvectl vms --templates ubuntu26
  pvectl vms 280 --all

  # programmatic (stable JSON array on stdout)
  pvectl vms --templates -o json | jq '.[] | {vmid, name, node}'
  pvectl vms k3s --details=false -o json`,
	Args: maxArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filter := ""
		if len(args) == 1 {
			filter = args[0]
		}
		if !cmd.Flags().Changed("details") {
			vmsFlags.details = filter != ""
		}
		kind := pve.KindVMs
		switch {
		case vmsFlags.all:
			kind = pve.KindAll
		case vmsFlags.templates:
			kind = pve.KindTemplates
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		rs, err := c.Resources(ctx)
		if err != nil {
			return err
		}
		found := pve.Filter(rs, filter, kind)
		rows := make([]VMRow, len(found))
		for i, r := range found {
			rows[i] = VMRow{Resource: r}
			if vmsFlags.details && !bool(r.Template) && r.Type == "qemu" {
				vmDetails(ctx, c, &rows[i], vmsFlags.days)
			}
		}
		if jsonOut() {
			if err := printJSON(rows); err != nil {
				return err
			}
		} else {
			printVMs(rows, filter)
		}
		if len(rows) == 0 {
			return silentErr(ExitNotFound, fmt.Errorf("no guest matching %q: %w", filter, pve.ErrNotFound))
		}
		return nil
	},
}

func vmDetails(ctx context.Context, c *pve.Client, row *VMRow, days int) {
	if row.Status == "running" {
		ips, err := c.GuestIPv4(ctx, row.Node, row.VMID)
		if err != nil {
			row.IPError = "agent: " + shortErr(err)
		}
		row.IPs = ips
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	ts, err := c.Tasks(ctx, pve.TaskFilter{Node: row.Node, VMID: row.VMID, Since: since, Limit: 100})
	if err != nil {
		return
	}
	var keep []pve.Task
	for _, t := range ts {
		if strings.HasPrefix(t.Type, "qm") || strings.HasPrefix(t.Type, "ha") {
			keep = append(keep, t)
		}
	}
	row.NumTasks = len(keep)
	if len(keep) > 10 {
		keep = keep[len(keep)-10:]
	}
	row.Tasks = keep
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && len(s)-i < 120 {
		return s[i+2:]
	}
	return s
}

func printVMs(rows []VMRow, filter string) {
	w := os.Stdout
	if len(rows) == 0 {
		return
	}
	detailed := false
	for _, r := range rows {
		if r.Tasks != nil || r.IPs != nil || r.IPError != "" {
			detailed = true
		}
	}
	if !detailed {
		var t [][]string
		for _, r := range rows {
			status := r.Status
			if r.Template {
				status = "template"
			}
			t = append(t, []string{fmt.Sprint(r.VMID), r.Name, r.Node, status, fmtUptime(r.Uptime), r.Tags})
		}
		table(w, []string{"VMID", "NAME", "NODE", "STATUS", "UPTIME", "TAGS"}, t)
		return
	}
	for _, r := range rows {
		ip := strings.Join(r.IPs, ",")
		if r.IPError != "" {
			ip = "(" + r.IPError + ")"
		}
		status := r.Status
		if r.Template {
			status = "template"
		}
		_, _ = fmt.Fprintf(w, "%-28s vmid=%d node=%s status=%s uptime=%s ip=%s tags=%s\n",
			keyStyle.Render(r.Name), r.VMID, r.Node, status, fmtUptime(r.Uptime), ip, r.Tags)
		for _, t := range r.Tasks {
			st := t.Status
			if t.Running() {
				st = "running"
			}
			_, _ = fmt.Fprintf(w, "    %s %-12s %s %s\n", fmtTime(t.StartTime), t.Type, t.User, st)
		}
		if r.NumTasks > len(r.Tasks) {
			_, _ = fmt.Fprintf(w, "    (%d in the last %d days)\n", r.NumTasks, vmsFlags.days)
		}
	}
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usageErr("%s takes at most %d argument(s), got %d: %s", cmd.Name(), n, len(args), strings.Join(args, " "))
		}
		return nil
	}
}

func init() {
	f := vmsCmd.Flags()
	f.BoolVar(&vmsFlags.templates, "templates", false, "list templates instead of VMs")
	f.BoolVar(&vmsFlags.all, "all", false, "list VMs and templates")
	f.BoolVar(&vmsFlags.details, "details", false, "guest IP and recent qm*/ha* tasks per VM (default: on with a filter)")
	f.IntVar(&vmsFlags.days, "days", 4, "task window for --details, in days")
	rootCmd.AddCommand(vmsCmd)
}
