package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

var tasksFlags struct {
	follow  string
	node    string
	vmid    int
	typ     string
	errors  bool
	limit   int
	since   time.Duration
	timeout time.Duration
}

// FollowResult is `tasks --follow -o json`.
type FollowResult struct {
	pve.TaskStatus
	Log []string `json:"log"`
}

var tasksCmd = &cobra.Command{
	Use:     "tasks",
	GroupID: groupRead,
	Short:   "List recent Proxmox tasks, or follow one task (UPID) until it ends",
	Long: `Without --follow: the recent tasks of the cluster (/cluster/tasks), or of
one node with --node (then --vmid, --since and --errors are passed to the API).

With --follow <UPID>: print the task log as it grows and wait until the task
stops. A finished task prints its whole log at once. Exit 5 if the task ended
with an error. With -o json the log lines go to stderr and stdout gets the
final status plus the full log.`,
	Example: `  # human
  pvectl tasks
  pvectl tasks --node ul-pve01 --vmid 280 --since 96h
  pvectl tasks --errors --type qm
  pvectl tasks --follow 'UPID:ul-pve01:0012ABCD:...:qmclone:280:user@pve:'

  # programmatic
  pvectl tasks --node ul-pve11 --type qmigrate -o json | jq '.[-1].status'
  pvectl tasks --follow "$UPID" -o json --timeout 2h || echo "task failed: exit $?"`,
	Args: maxArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := newClient()
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("follow") && tasksFlags.follow == "" {
			return &cliError{code: ExitUsage, flag: "--follow", err: fmt.Errorf("--follow needs a UPID")}
		}
		if tasksFlags.follow != "" {
			if _, err := pve.ParseUPID(tasksFlags.follow); err != nil {
				return &cliError{code: ExitUsage, flag: "--follow", err: err}
			}
			if tasksFlags.timeout > 0 {
				var cancel func()
				ctx, cancel = contextWithTimeout(ctx, tasksFlags.timeout)
				defer cancel()
			}
			var log []string
			var st pve.TaskStatus
			if jsonOut() || !interactive() {
				st, err = c.WaitTask(ctx, tasksFlags.follow, pve.WaitOptions{OnEvent: func(ev pve.TaskEvent) {
					log = append(log, ev.Line)
					if jsonOut() {
						_, _ = fmt.Fprintln(os.Stderr, ev.Line)
					} else {
						fmt.Println(ev.Line)
					}
				}})
			} else {
				st, err = waitTask(ctx, c, "task "+shortUPID(tasksFlags.follow), tasksFlags.follow)
			}
			if jsonOut() && st.UPID != "" {
				if log == nil {
					log = []string{}
				}
				if perr := printJSON(FollowResult{TaskStatus: st, Log: log}); perr != nil {
					return perr
				}
				if err != nil {
					return silentErr(exitCode(err), err)
				}
				return nil
			}
			if err == nil && !jsonOut() {
				fmt.Printf("%s %s %s: %s\n", mark(true), st.Type, st.ID, st.ExitStatus)
			}
			return err
		}

		f := pve.TaskFilter{Node: tasksFlags.node, VMID: tasksFlags.vmid, Type: tasksFlags.typ,
			Errors: tasksFlags.errors, Limit: tasksFlags.limit}
		if tasksFlags.since > 0 {
			f.Since = time.Now().Add(-tasksFlags.since)
		}
		ts, err := c.Tasks(ctx, f)
		if err != nil {
			return err
		}
		if jsonOut() {
			if ts == nil {
				ts = []pve.Task{}
			}
			return printJSON(ts)
		}
		var rows [][]string
		for _, t := range ts {
			st := t.Status
			if t.Running() {
				st = "running"
			}
			rows = append(rows, []string{fmtTime(t.StartTime), t.Node, t.Type, t.ID, t.User, st, t.UPID})
		}
		table(os.Stdout, []string{"START", "NODE", "TYPE", "ID", "USER", "STATUS", "UPID"}, rows)
		return nil
	},
}

func shortUPID(upid string) string {
	u, err := pve.ParseUPID(upid)
	if err != nil {
		return upid
	}
	return fmt.Sprintf("%s %s on %s", u.Type, u.ID, u.Node)
}

func init() {
	f := tasksCmd.Flags()
	f.StringVar(&tasksFlags.follow, "follow", "", "UPID of a task to follow until it ends")
	f.StringVar(&tasksFlags.node, "node", "", "only tasks of this node")
	f.IntVar(&tasksFlags.vmid, "vmid", 0, "only tasks of this VMID")
	f.StringVar(&tasksFlags.typ, "type", "", "only task types with this prefix (qm, qmigrate, qmclone, ha...)")
	f.BoolVar(&tasksFlags.errors, "errors", false, "only failed tasks")
	f.IntVar(&tasksFlags.limit, "limit", 50, "at most this many tasks (the newest)")
	f.DurationVar(&tasksFlags.since, "since", 0, "only tasks started within this duration (e.g. 96h)")
	f.DurationVar(&tasksFlags.timeout, "timeout", 0, "with --follow: stop waiting after this duration (the task keeps running)")
	rootCmd.AddCommand(tasksCmd)
}
