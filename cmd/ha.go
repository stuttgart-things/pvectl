package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// startTasks are the task types whose log `ha` shows (pve.py START_TASKS).
var startTasks = map[string]bool{"hastart": true, "qmstart": true, "qmstop": true, "qmshutdown": true, "hamigrate": true, "qmigrate": true}

// HAResult is `ha -o json`.
type HAResult struct {
	VMID     int             `json:"vmid"`
	Node     string          `json:"node"`
	Status   string          `json:"status"`
	Resource *pve.HAResource `json:"ha_resource,omitempty"`
	HA       []pve.HAStatus  `json:"ha_status"`
	Tasks    []pve.Task      `json:"tasks"`
	LastLog  *struct {
		Task pve.Task `json:"task"`
		Log  []string `json:"log"`
	} `json:"last_start_log,omitempty"`
}

// guestArgs resolves [<node>] <vmid> or --vmid; the node is looked up and,
// if given, must match.
func guestArgs(ctx context.Context, c *pve.Client, args []string, flagID int, kind pve.Kind, title string) (pve.Resource, error) {
	switch len(args) {
	case 2:
		r, err := resolveGuest(ctx, c, args[1:], flagID, "--vmid", kind, title)
		if err != nil {
			return r, err
		}
		if r.Node != args[0] {
			return r, fmt.Errorf("VMID %d is on %s, not on %s: %w", r.VMID, r.Node, args[0], pve.ErrNotFound)
		}
		return r, nil
	default:
		return resolveGuest(ctx, c, args, flagID, "--vmid", kind, title)
	}
}

var haFlags struct{ vmid int }

var haCmd = &cobra.Command{
	Use:     "ha [<node>] [vmid]",
	GroupID: groupRead,
	Short:   "Show the HA resource and state of a VM, its recent tasks and the log of the last start/migrate",
	Long: `Read-only HA view of one VM (pve.py ha): the HA resource (its comment is
left out), the HA status of the service plus quorum/master, the VM status,
the last tasks, and the log of the last start/stop/migrate task.`,
	Example: `  pvectl ha ul-pve10 227
  pvectl ha 227 -o json | jq '.ha_status'`,
	Args: maxArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := newClient()
		if err != nil {
			return err
		}
		r, err := guestArgs(ctx, c, args, haFlags.vmid, pve.KindVMs, "Which VM?")
		if err != nil {
			return err
		}
		sid := pve.HASID(r.VMID)
		res := HAResult{VMID: r.VMID, Node: r.Node, HA: []pve.HAStatus{}, Tasks: []pve.Task{}}
		if hr, err := c.HAResource(ctx, sid); err == nil {
			res.Resource = &hr
		}
		all, err := c.HAStatusAll(ctx)
		if err != nil {
			return err
		}
		for _, s := range all {
			if s.SID == sid || s.Type == "quorum" || s.Type == "master" {
				res.HA = append(res.HA, s)
			}
		}
		st, err := c.Status(ctx, r.Node, r.VMID)
		if err != nil {
			return err
		}
		res.Status = st.Status
		ts, _ := c.Tasks(ctx, pve.TaskFilter{Node: r.Node, VMID: r.VMID, Limit: 50})
		if len(ts) > 8 {
			res.Tasks = ts[len(ts)-8:]
		} else if ts != nil {
			res.Tasks = ts
		}
		for i := len(ts) - 1; i >= 0; i-- {
			if startTasks[ts[i].Type] {
				lines, err := c.TaskLog(ctx, ts[i].UPID, 0, 60)
				if err == nil {
					res.LastLog = &struct {
						Task pve.Task `json:"task"`
						Log  []string `json:"log"`
					}{Task: ts[i]}
					for _, l := range lines {
						res.LastLog.Log = append(res.LastLog.Log, l.T)
					}
				}
				break
			}
		}
		if jsonOut() {
			return printJSON(res)
		}
		w := os.Stdout
		if res.Resource != nil {
			fmt.Fprintf(w, "%s %s state=%s group=%s max_restart=%d max_relocate=%d\n", keyStyle.Render("HA resource:"), sid,
				res.Resource.State, res.Resource.Group, res.Resource.MaxRestart, res.Resource.MaxRelocate)
		} else {
			fmt.Fprintf(w, "%s none\n", keyStyle.Render("HA resource:"))
		}
		for _, s := range res.HA {
			fmt.Fprintf(w, "%s type=%s sid=%s node=%s state=%s request_state=%s status=%s\n", keyStyle.Render("HA status:  "),
				s.Type, s.SID, s.Node, s.State, s.RequestState, s.Status)
		}
		fmt.Fprintf(w, "%s %s\n", keyStyle.Render("VM status:  "), res.Status)
		for _, t := range res.Tasks {
			st := t.Status
			if t.Running() {
				st = "running"
			}
			fmt.Fprintf(w, "    %s %-12s %s %s\n", fmtTime(t.StartTime), t.Type, t.User, st)
		}
		if res.LastLog != nil {
			fmt.Fprintf(w, "log of the last %s:\n", res.LastLog.Task.Type)
			for _, l := range res.LastLog.Log {
				fmt.Fprintln(w, "    "+l)
			}
		}
		return nil
	},
}

var startFlags struct {
	vmid   int
	dryRun bool
	yes    bool
	wait   time.Duration
}

// StartResult is `start` / `ha-recover -o json`.
type StartResult struct {
	VMID    int      `json:"vmid"`
	Node    string   `json:"node"`
	Before  string   `json:"before"`
	State   string   `json:"state"` // planned | skipped | done | no_ip
	Reason  string   `json:"reason,omitempty"`
	Calls   []string `json:"calls,omitempty"`
	UPID    string   `json:"upid,omitempty"`
	IPs     []string `json:"ips,omitempty"`
	HAState string   `json:"ha_state,omitempty"`
}

var startCmd = &cobra.Command{
	Use:     "start [<node>] [vmid]",
	GroupID: groupWrite,
	Short:   "Start a stopped VM and wait for its guest IP (refuses when HA is in error)",
	Long: `Start a stopped VM and wait (--wait, default 3m) for a guest-agent IPv4
(pve.py start). A VM that is not stopped is left alone (exit 0, state
skipped). HA state error refuses any start: use ha-recover.`,
	Example: `  pvectl start ul-pve10 227
  pvectl start --vmid 227 --dry-run -o json
  pvectl start --vmid 227 --yes -o json`,
	Args: maxArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := newClient()
		if err != nil {
			return err
		}
		r, err := guestArgs(ctx, c, args, startFlags.vmid, pve.KindVMs, "Which VM to start?")
		if err != nil {
			return err
		}
		st, err := c.Status(ctx, r.Node, r.VMID)
		if err != nil {
			return err
		}
		res := StartResult{VMID: r.VMID, Node: r.Node, Before: st.Status}
		if st.Status != "stopped" {
			res.State, res.Reason = "skipped", "not stopped ("+st.Status+"), leaving it alone"
			return reportStart(res)
		}
		if ha, ok, err := c.HAState(ctx, pve.HASID(r.VMID)); err == nil && ok && ha.State == "error" {
			return fmt.Errorf("HA state of %s is error, a start is refused -- use: pvectl ha-recover %s %d", ha.SID, r.Node, r.VMID)
		}
		res.Calls = []string{fmt.Sprintf("POST /nodes/%s/qemu/%d/status/start", r.Node, r.VMID)}
		res.State = "planned"
		proceed, err := writeGate(ctx, startFlags.dryRun, startFlags.yes, fmt.Sprintf("start %d", r.VMID),
			[]string{fmt.Sprintf("start %d %s on %s", r.VMID, r.Name, r.Node)})
		if err != nil {
			return err
		}
		if !proceed {
			return reportStart(res)
		}
		upid, err := c.Start(ctx, r.Node, r.VMID)
		if err != nil {
			return err
		}
		res.UPID = upid
		logf("start task: %s", upid)
		res.IPs = waitIP(ctx, c, r.Node, r.VMID, startFlags.wait)
		res.State = "done"
		if len(res.IPs) == 0 {
			res.State = "no_ip"
		}
		return reportStart(res)
	},
}

var haRecoverCmd = &cobra.Command{
	Use:     "ha-recover [<node>] [vmid]",
	GroupID: groupWrite,
	Short:   "HA state error -> disabled -> started, then wait for the guest IP",
	Long: `An HA resource in state error refuses any start ("must be disabled and fixed
first"). ha-recover sets it disabled, waits until HA reports disabled/stopped,
sets it started and waits for a guest-agent IPv4 (pve.py ha-recover; #4240).
A service that is not in error is left alone.`,
	Example: `  pvectl ha-recover ul-pve10 227
  pvectl ha-recover --vmid 227 --dry-run -o json
  pvectl ha-recover --vmid 227 --yes -o json`,
	Args: maxArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := newClient()
		if err != nil {
			return err
		}
		r, err := guestArgs(ctx, c, args, startFlags.vmid, pve.KindVMs, "Which VM to recover?")
		if err != nil {
			return err
		}
		sid := pve.HASID(r.VMID)
		ha, ok, err := c.HAState(ctx, sid)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s is not under HA: %w", sid, pve.ErrNotFound)
		}
		res := StartResult{VMID: r.VMID, Node: r.Node, Before: ha.State}
		if ha.State != "error" {
			res.State, res.Reason = "skipped", "HA service not in error ("+ha.State+"), leaving it alone"
			return reportStart(res)
		}
		res.Calls = []string{pve.HASetStateCall(sid, "disabled").String(), pve.HASetStateCall(sid, "started").String()}
		res.State = "planned"
		proceed, err := writeGate(ctx, startFlags.dryRun, startFlags.yes, "ha-recover "+sid,
			[]string{fmt.Sprintf("%s (%s on %s): error -> disabled -> started", sid, r.Name, r.Node)})
		if err != nil {
			return err
		}
		if !proceed {
			return reportStart(res)
		}
		if err := c.HASetState(ctx, sid, "disabled"); err != nil {
			return err
		}
		logf("set %s state disabled", sid)
		for i := 0; i < 24; i++ {
			if err := sleepCtx(ctx, 5*time.Second); err != nil {
				return err
			}
			if s, _, err := c.HAState(ctx, sid); err == nil && (s.State == "disabled" || s.State == "stopped") {
				break
			}
		}
		if err := c.HASetState(ctx, sid, "started"); err != nil {
			return err
		}
		logf("set %s state started", sid)
		res.IPs = waitIP(ctx, c, r.Node, r.VMID, startFlags.wait+time.Minute)
		if s, _, err := c.HAState(ctx, sid); err == nil {
			res.HAState = s.State
		}
		res.State = "done"
		if len(res.IPs) == 0 {
			res.State = "no_ip"
		}
		return reportStart(res)
	},
}

func reportStart(res StartResult) error {
	if jsonOut() {
		return printJSON(res)
	}
	switch res.State {
	case "skipped":
		fmt.Printf("%s %s\n", okStyle.Render("nothing to do:"), res.Reason)
	case "planned":
		fmt.Printf("%s %d on %s (now %s)\n", warnStyle.Render("dry-run:"), res.VMID, res.Node, res.Before)
		for _, c := range res.Calls {
			fmt.Println("  " + c)
		}
	case "done":
		fmt.Printf("%s %d running, ip: %v %s\n", mark(true), res.VMID, res.IPs, res.HAState)
	case "no_ip":
		fmt.Printf("%s %d: no guest-agent IP yet %s\n", warnStyle.Render("!"), res.VMID, res.HAState)
	}
	return nil
}

// waitIP polls the guest agent until it reports an IPv4 or d passes.
func waitIP(ctx context.Context, c *pve.Client, node string, vmid int, d time.Duration) []string {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if sleepCtx(ctx, 5*time.Second) != nil {
			return nil
		}
		if ips, err := c.GuestIPv4(ctx, node, vmid); err == nil && len(ips) > 0 {
			return ips
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func init() {
	haCmd.Flags().IntVar(&haFlags.vmid, "vmid", 0, "VMID (same as the argument)")
	for _, c := range []*cobra.Command{startCmd, haRecoverCmd} {
		f := c.Flags()
		f.IntVar(&startFlags.vmid, "vmid", 0, "VMID (same as the argument)")
		f.BoolVar(&startFlags.dryRun, "dry-run", false, "show the API calls, change nothing")
		f.BoolVarP(&startFlags.yes, "yes", "y", false, "skip the confirmation (required without a TTY)")
		f.DurationVar(&startFlags.wait, "wait", 3*time.Minute, "how long to wait for the guest IP")
	}
	rootCmd.AddCommand(haCmd, startCmd, haRecoverCmd)
}
