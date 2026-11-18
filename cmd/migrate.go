package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

var migrateFlags struct {
	node          string
	vmid          int
	target        string
	targetStorage string
	dryRun        bool
	yes           bool
	timeout       time.Duration
}

// MigrateResult is `migrate -o json`.
type MigrateResult struct {
	VMID       int               `json:"vmid"`
	Name       string            `json:"name"`
	From       string            `json:"from"`
	Target     string            `json:"target"`
	Status     string            `json:"status"` // VM status before
	LocalDisks []string          `json:"local_disks"`
	Request    pve.APICall       `json:"request"`
	State      string            `json:"state"` // planned | skipped | done
	Reason     string            `json:"reason,omitempty"`
	UPID       string            `json:"upid,omitempty"`
	Task       *pve.TaskStatus   `json:"task,omitempty"`
	After      map[string]string `json:"after,omitempty"`
}

var migrateCmd = &cobra.Command{
	Use:     "migrate [<node> <vmid> <target>]",
	GroupID: groupWrite,
	Short:   "Move a VM or template to another node and wait for the task",
	Long: `Move a VM or template: online if it runs, with its local disks if it has any,
then wait for the task and show where the guest ended up.

Arguments as in pve.py (<node> <vmid> <target>), or as flags (--vmid, --target;
--node is looked up when left out). Interactive mode asks for whatever is
missing: the guest from a filterable list, the target node, and a target
storage if the target lacks the guest's storage.

A guest already on the target is reported, not migrated (exit 0).`,
	Example: `  # human: asks for guest, target (and storage), shows a summary, confirms
  pvectl migrate
  pvectl migrate ul-pve01 280 ul-pve11

  # programmatic
  pvectl migrate --vmid 280 --target ul-pve11 --dry-run -o json
  pvectl migrate --vmid 280 --target ul-pve11 --yes -o json
  pvectl migrate ul-pve02 227 ul-pve10 --targetstorage local-lvm --yes`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 && len(args) != 3 {
			return usageErr("migrate takes <node> <vmid> <target> or flags, got %d argument(s)", len(args))
		}
		return nil
	},
	RunE: runMigrate,
}

func runMigrate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	f := migrateFlags
	if len(args) == 3 {
		id, err := strconv.Atoi(args[1])
		if err != nil {
			return &cliError{code: ExitUsage, flag: "--vmid", err: fmt.Errorf("VMID must be a number, not %q", args[1])}
		}
		f.node, f.vmid, f.target = args[0], id, args[2]
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var r pve.Resource
	if f.vmid == 0 {
		if !interactive() {
			return missingFlag("--vmid", "VMID")
		}
		if r, err = pickGuest(ctx, c, pve.KindAll, "Which VM or template to migrate?"); err != nil {
			return err
		}
	} else if r, err = c.FindVM(ctx, f.vmid); err != nil {
		return err
	}
	if f.node != "" && f.node != r.Node {
		if f.target != "" && r.Node == f.target {
			return reportMigrate(MigrateResult{VMID: r.VMID, Name: r.Name, From: f.node, Target: f.target, Status: r.Status,
				LocalDisks: []string{}, State: "skipped", Reason: fmt.Sprintf("%d is already on %s", r.VMID, f.target)})
		}
		return fmt.Errorf("VMID %d is on %s, not on %s: %w", r.VMID, r.Node, f.node, pve.ErrNotFound)
	}
	if f.target == "" {
		if !interactive() {
			return missingFlag("--target", "target node")
		}
		if f.target, err = pickNode(ctx, c, fmt.Sprintf("Move %d %s from %s to?", r.VMID, r.Name, r.Node), r.Node, ""); err != nil {
			return err
		}
	}
	res := MigrateResult{VMID: r.VMID, Name: r.Name, From: r.Node, Target: f.target, Status: r.Status, LocalDisks: []string{}}
	if r.Node == f.target {
		res.State, res.Reason = "skipped", fmt.Sprintf("%d is already on %s", r.VMID, f.target)
		return reportMigrate(res)
	}

	mc, err := c.MigrateCheck(ctx, r.Node, r.VMID, f.target)
	if err != nil {
		return err
	}
	if mc.LocalDisks != nil {
		res.LocalDisks = mc.LocalDisks
	}
	if b := mc.Blocked(f.target); b != nil {
		missing := mc.UnavailableStorages(f.target)
		if len(missing) > 0 && f.targetStorage == "" && interactive() {
			title := fmt.Sprintf("%s lacks %s: target storage on %s?", f.target, strings.Join(missing, ", "), f.target)
			if f.targetStorage, err = pickStorage(ctx, c, f.target, title, false, ""); err != nil {
				return err
			}
		}
		if len(missing) == 0 || f.targetStorage == "" {
			raw, _ := json.Marshal(b)
			if len(missing) > 0 {
				return &cliError{code: ExitUsage, flag: "--targetstorage",
					err: fmt.Errorf("%s not allowed: %s -- pass --targetstorage <storage on %s>", f.target, raw, f.target)}
			}
			return fmt.Errorf("%s not allowed for %d: %s", f.target, r.VMID, raw)
		}
	}
	req := pve.MigrateRequest{Node: r.Node, VMID: r.VMID, Target: f.target, TargetStorage: f.targetStorage,
		Online: mc.Running, WithLocalDisks: len(mc.LocalDisks) > 0}
	res.Request = req.Call()
	res.State = "planned"

	mode := "offline"
	if req.Online {
		mode = "online"
	}
	summary := []string{
		fmt.Sprintf("%s %d %s", keyStyle.Render("guest  "), r.VMID, r.Name),
		fmt.Sprintf("%s %s -> %s (%s, status %s)", keyStyle.Render("move   "), r.Node, f.target, mode, r.Status),
		fmt.Sprintf("%s %s", keyStyle.Render("disks  "), orNone(mc.LocalDisks)),
		fmt.Sprintf("%s %s", keyStyle.Render("request"), res.Request.String()),
	}
	proceed, err := writeGate(ctx, f.dryRun, f.yes, fmt.Sprintf("migrate %d", r.VMID), summary)
	if err != nil {
		return err
	}
	if !proceed {
		return reportMigrate(res)
	}

	upid, err := c.Migrate(ctx, req)
	if err != nil {
		return err
	}
	res.UPID = upid
	wctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	st, werr := waitTask(wctx, c, fmt.Sprintf("migrate %d %s -> %s", r.VMID, r.Node, f.target), upid)
	res.Task = &st
	if werr != nil {
		res.State = "failed"
		res.Reason = werr.Error()
		if jsonOut() {
			_ = printJSON(res)
			return silentErr(exitCode(werr), werr)
		}
		return werr
	}
	res.State = "done"
	if now, err := c.FindVM(ctx, r.VMID); err == nil {
		res.After = map[string]string{"node": now.Node, "status": now.Status}
	}
	return reportMigrate(res)
}

func reportMigrate(res MigrateResult) error {
	if jsonOut() {
		return printJSON(res)
	}
	w := os.Stdout
	switch res.State {
	case "skipped":
		fmt.Fprintf(w, "%s %s\n", okStyle.Render("nothing to do:"), res.Reason)
	case "planned":
		fmt.Fprintf(w, "%s migrate %d %s %s -> %s\n", warnStyle.Render("dry-run:"), res.VMID, res.Name, res.From, res.Target)
		fmt.Fprintf(w, "  local disks: %s\n  %s\n", orNone(res.LocalDisks), res.Request.String())
	case "done":
		fmt.Fprintf(w, "%s %d %s now on %s (status %s)\n", mark(true), res.VMID, res.Name, res.After["node"], res.After["status"])
	}
	return nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func contextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func init() {
	f := migrateCmd.Flags()
	f.StringVar(&migrateFlags.node, "node", "", "node the guest is on (looked up when left out)")
	f.IntVar(&migrateFlags.vmid, "vmid", 0, "VMID to migrate")
	f.StringVar(&migrateFlags.target, "target", "", "target node")
	f.StringVar(&migrateFlags.targetStorage, "targetstorage", "", "storage on the target node for local disks")
	f.BoolVar(&migrateFlags.dryRun, "dry-run", false, "show the plan (API call), change nothing")
	f.BoolVarP(&migrateFlags.yes, "yes", "y", false, "skip the confirmation (required without a TTY)")
	f.DurationVar(&migrateFlags.timeout, "timeout", 90*time.Minute, "stop waiting after this duration (the task keeps running)")
	rootCmd.AddCommand(migrateCmd)
}
