package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/pkg/promote"
	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/repin"
)

var templateCmd = &cobra.Command{
	Use:     "template",
	GroupID: groupWrite,
	Short:   "Template rounds: promote a fresh Packer template",
}

var promoteFlags struct {
	vmid          int
	to            string
	targetStorage string
	copy          bool
	noCopy        bool
	copyNode      string
	copyStorage   string
	copyPool      string
	os            string
	repo          string
	skipChecks    bool
	dryRun        bool
	yes           bool
	timeout       time.Duration
}

var promoteCmd = &cobra.Command{
	Use:   "promote [vmid]",
	Short: "Check a template, migrate it to ul-pve11, copy it to FS9100 for Terraform, print the repins",
	Long: `Promote a template after a Packer build (on ul-pve01):

  1. checks      cpu: host, guest agent, cloud-init drive (exit 4 on failure;
                 --skip-checks to override)
  2. migrate     (--to) the template to ul-pve11, so the proxmoxvm chart can
                 leave templateNode empty.
  3. FS9100 copy (--fs9100-copy) a full clone named <name>-fs9100 on
                 FS9100-01-1 (shared), target node ul-pve01, pool
                 stuttgart-things, converted to a template. Telmate Terraform
                 sends no storage on clone and gets 403 (Datastore.AllocateSpace)
                 on DD-sthings. Same calls as pve-fs9100-copy.py (282, 2026-11-16).
  4. repins      the values to change in stuttgart-things (pvectl does not edit
                 or open PRs): proxmoxvm values.yaml templateVmId (+ chart bump,
                 flux release, flux-infra tag) and vm_clone_id in both
                 create-terraform-vm files. --repo <checkout> adds the current
                 values.

Idempotent: a template already on --to is not migrated, a copy that exists
by name is reported and not cloned again. Run it again after a failure.

Writes need confirmation (interactive) or --yes (programmatic); --dry-run
shows the plan with the API calls and the new VMID/name and changes nothing.`,
	Example: `  # human: asks for template, target node and copy storage, shows the plan, confirms
  pvectl template promote
  pvectl template promote 280 --to ul-pve11 --fs9100-copy

  # programmatic
  pvectl template promote 280 --to ul-pve11 --fs9100-copy --dry-run -o json
  pvectl template promote 280 --to ul-pve11 --fs9100-copy --yes -o json \
    --repo ~/projects/stuttgart-things | jq '.repins'`,
	Args: maxArgs(1),
	RunE: runPromote,
}

// PromoteResult is `template promote -o json`.
type PromoteResult struct {
	DryRun    bool          `json:"dry_run"`
	Plan      *promote.Plan `json:"plan"`
	FollowUps []string      `json:"follow_ups"`
	Error     string        `json:"error,omitempty"`
}

func runPromote(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	f := promoteFlags
	c, err := newClient()
	if err != nil {
		return err
	}
	r, err := resolveGuest(ctx, c, args, f.vmid, "--vmid", pve.KindTemplates, "Which template to promote?")
	if err != nil {
		return err
	}
	flags := cmd.Flags()
	if interactive() {
		if !flags.Changed("to") {
			if f.to, err = pickNode(ctx, c, fmt.Sprintf("Move template %d (now on %s) to? (pick %s to leave it)", r.VMID, r.Node, r.Node), "", promote.DefaultTarget); err != nil {
				return err
			}
		}
		if !flags.Changed("fs9100-copy") && !flags.Changed("no-fs9100-copy") {
			ok := true
			q := huh.NewConfirm().Title("Full copy on shared storage for Telmate Terraform (vm_clone_id)?").Value(&ok)
			if err := huh.NewForm(huh.NewGroup(q)).RunWithContext(ctx); err != nil {
				return err
			}
			f.copy = ok
		}
		if f.copy && !flags.Changed("copy-storage") {
			if f.copyStorage, err = pickStorage(ctx, c, f.copyNode, "Storage for the copy (shared)?", true, promote.DefaultCopyStorage); err != nil {
				return err
			}
		}
	} else if !flags.Changed("to") && !flags.Changed("fs9100-copy") {
		return missingFlag("--to", "what to do (--to <node> and/or --fs9100-copy)")
	}
	if f.noCopy {
		f.copy = false
	}

	o := promote.Options{SourceID: r.VMID, To: f.to, TargetStorage: f.targetStorage, Copy: f.copy,
		CopyNode: f.copyNode, CopyStorage: f.copyStorage, CopyPool: f.copyPool, OS: f.os, SkipChecks: f.skipChecks}
	plan, err := promote.Build(ctx, c, o)
	res := PromoteResult{DryRun: f.dryRun, Plan: plan, FollowUps: repin.FollowUps}
	if plan != nil && f.repo != "" {
		if rerr := repin.ReadCurrent(f.repo, plan.Repins); rerr != nil {
			plan.Notes = append(plan.Notes, "repins: "+rerr.Error())
		}
	}
	if err != nil {
		if plan == nil {
			return err
		}
		res.Error = err.Error()
		emitPromote(res)
		return silentErr(exitCode(err), err)
	}

	if !plan.Pending() {
		emitPromote(res)
		return nil
	}
	if !jsonOut() && (f.dryRun || interactive()) && !f.yes {
		printPlan(os.Stdout, plan, false)
	}
	proceed, err := writeGate(ctx, f.dryRun, f.yes, fmt.Sprintf("promote %d %s", plan.Source.VMID, plan.Source.Name),
		[]string{"the steps above write to Proxmox"})
	if err != nil {
		return err
	}
	if !proceed {
		if jsonOut() {
			return printJSON(res)
		}
		printRepins(os.Stdout, plan)
		return nil
	}

	wctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	execErr := promote.Execute(wctx, c, plan, &promoteProgress{c: c})
	if execErr != nil {
		res.Error = execErr.Error()
	}
	emitPromote(res)
	if execErr != nil {
		return silentErr(exitCode(execErr), execErr)
	}
	return nil
}

func emitPromote(res PromoteResult) {
	if jsonOut() {
		_ = printJSON(res)
		return
	}
	printPlan(os.Stdout, res.Plan, true)
	printRepins(os.Stdout, res.Plan)
}

func printPlan(w io.Writer, p *promote.Plan, final bool) {
	_, _ = fmt.Fprintf(w, "%s %s\n", titleStyle.Render(fmt.Sprintf("%d %s", p.Source.VMID, p.Source.Name)), dimStyle.Render("on "+p.Source.Node))
	printSection(w, "checks")
	for _, ch := range p.Checks {
		_, _ = fmt.Fprintf(w, "%s %-17s %s\n", checkMark(ch), ch.Name, ch.Detail)
	}
	if len(p.Steps) > 0 {
		title := "plan"
		if final {
			title = "steps"
		}
		printSection(w, title)
		for _, s := range p.Steps {
			state := s.State
			switch s.State {
			case promote.StateDone:
				state = okStyle.Render(state)
			case promote.StateFailed:
				state = errorStyle.Render(state)
			case promote.StateSkipped:
				state = dimStyle.Render(state)
			default:
				state = warnStyle.Render(state)
			}
			_, _ = fmt.Fprintf(w, "%-8s %s\n", state, s.Title)
			if s.Reason != "" {
				_, _ = fmt.Fprintf(w, "         %s\n", dimStyle.Render(s.Reason))
			}
			if s.State == promote.StatePlanned {
				for _, call := range s.Calls {
					_, _ = fmt.Fprintf(w, "         %s\n", dimStyle.Render(call.String()))
				}
			}
		}
	}
	if p.Copy != nil {
		_, _ = fmt.Fprintf(w, "\n%s %d %s on %s\n", keyStyle.Render("copy:"), p.Copy.VMID, p.Copy.Name, p.Copy.Node)
	}
	for _, n := range p.Notes {
		_, _ = fmt.Fprintf(w, "%s %s\n", dimStyle.Render("note:"), n)
	}
}

func printRepins(w io.Writer, p *promote.Plan) {
	if p == nil || len(p.Repins) == 0 {
		return
	}
	printSection(w, "repins (stuttgart-things, by PR)")
	printChanges(w, p.Repins)
	_, _ = fmt.Fprintln(w, dimStyle.Render("then: "+strings.Join(repin.FollowUps, "; ")))
}

func printChanges(w io.Writer, cs []repin.Change) {
	for _, c := range cs {
		cur := ""
		switch {
		case c.Current == "":
		case c.Unchanged():
			cur = dimStyle.Render(" (already " + c.Current + ")")
		default:
			cur = dimStyle.Render(" (now " + c.Current + ")")
		}
		_, _ = fmt.Fprintf(w, "%s\n  %s = %s%s\n", keyStyle.Render(c.File), c.Path, okStyle.Render(strconv.Quote(c.Value)), cur)
		if c.Note != "" {
			_, _ = fmt.Fprintf(w, "  %s\n", dimStyle.Render(c.Note))
		}
	}
}

// promoteProgress shows the steps of Execute.
type promoteProgress struct {
	c *pve.Client
}

func (p *promoteProgress) StepStarted(s promote.Step) { logf("▶ %s", s.Title) }
func (p *promoteProgress) TaskStarted(s promote.Step, upid string) {
	logf("  task %s", upid)
}
func (p *promoteProgress) TaskEvent(s promote.Step, ev pve.TaskEvent) { streamLine(ev.Line) }
func (p *promoteProgress) StepDone(s promote.Step) {
	endStream()
	if s.State == promote.StateDone {
		_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", mark(true), s.Title)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", mark(false), s.Title)
	}
}

var repinFlags struct {
	os         string
	templateID int
	copyID     int
	repo       string
}

var repinCmd = &cobra.Command{
	Use:     "repin",
	GroupID: groupRead,
	Short:   "Print the stuttgart-things changes for a new template (and FS9100 copy) VMID",
	Long: `Print what to change in stuttgart-things after a promotion (template promote
prints the same at its end). Nothing is edited: the PRs are made by hand.

  ubuntu26  values.yaml environments.labul.templateVmId and
            environments.labul.templates.ubuntu26.templateVmId
  <os>      values.yaml environments.labul.templates.<os>.templateVmId
  --copy    vm_clone_id default in create-terraform-vm/template.yaml and
            machinery-cluster-labul.yaml (both together)

--repo <checkout> reads the current values.`,
	Example: `  pvectl repin --os ubuntu26 --template 280 --copy 282 --repo ~/projects/stuttgart-things
  pvectl repin --os rocky10 --template 279 -o json`,
	Args: maxArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		if repinFlags.templateID == 0 {
			return missingFlag("--template", "template VMID")
		}
		if repinFlags.os == "" {
			return missingFlag("--os", "OS key (ubuntu26, rocky10, ...)")
		}
		cs := repin.ForPromote(repinFlags.os, repinFlags.templateID, repinFlags.copyID)
		if repinFlags.repo != "" {
			if err := repin.ReadCurrent(repinFlags.repo, cs); err != nil {
				return err
			}
		}
		if jsonOut() {
			return printJSON(map[string]any{"repins": cs, "follow_ups": repin.FollowUps})
		}
		printChanges(os.Stdout, cs)
		fmt.Println(dimStyle.Render("then: " + strings.Join(repin.FollowUps, "; ")))
		return nil
	},
}

func init() {
	f := promoteCmd.Flags()
	f.IntVar(&promoteFlags.vmid, "vmid", 0, "template VMID (same as the argument)")
	f.StringVar(&promoteFlags.to, "to", "", "migrate the template to this node (LabUL: "+promote.DefaultTarget+")")
	f.StringVar(&promoteFlags.targetStorage, "target-storage", "", "storage on --to if it lacks the template's storage")
	f.BoolVar(&promoteFlags.copy, "fs9100-copy", false, "full copy on shared storage for Telmate Terraform")
	f.BoolVar(&promoteFlags.noCopy, "no-fs9100-copy", false, "no copy (and no question about it)")
	f.StringVar(&promoteFlags.copyNode, "copy-node", promote.DefaultCopyNode, "node of the copy")
	f.StringVar(&promoteFlags.copyStorage, "copy-storage", promote.DefaultCopyStorage, "shared storage of the copy")
	f.StringVar(&promoteFlags.copyPool, "copy-pool", promote.DefaultCopyPool, "pool of the copy")
	f.StringVar(&promoteFlags.os, "os", "", "repin key (default: from the template name, e.g. ubuntu26)")
	f.StringVar(&promoteFlags.repo, "repo", "", "stuttgart-things checkout to read the current repin values from")
	f.BoolVar(&promoteFlags.skipChecks, "skip-checks", false, "promote even if a config check fails")
	f.BoolVar(&promoteFlags.dryRun, "dry-run", false, "show the plan (API calls, new VMID/name), change nothing")
	f.BoolVarP(&promoteFlags.yes, "yes", "y", false, "skip the confirmation (required without a TTY)")
	f.DurationVar(&promoteFlags.timeout, "timeout", 3*time.Hour, "stop waiting after this duration (tasks keep running)")
	templateCmd.AddCommand(promoteCmd)
	rootCmd.AddCommand(templateCmd)

	rf := repinCmd.Flags()
	rf.StringVar(&repinFlags.os, "os", "", "OS key: ubuntu26, rocky10, ...")
	rf.IntVar(&repinFlags.templateID, "template", 0, "VMID of the promoted template")
	rf.IntVar(&repinFlags.copyID, "copy", 0, "VMID of the FS9100 copy (vm_clone_id)")
	rf.StringVar(&repinFlags.repo, "repo", "", "stuttgart-things checkout to read the current values from")
	rootCmd.AddCommand(repinCmd)
}
