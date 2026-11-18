// Package promote plans and runs `pvectl template promote`: after a Packer
// build on ul-pve01, (1) optionally full-copy the template onto shared
// storage (FS9100-01-1) for Telmate Terraform, and (2) migrate the template
// to the node the proxmoxvm chart builds on (ul-pve11), so templateNode can
// stay unset.
//
// Order as in the 280/282 round (2026-11-16): migrate first, then clone the
// copy from wherever the template now is, with `target` = the copy node and
// `storage` = the shared storage. For a full clone with `storage`, PVE
// judges "shared" by the target storage, so a source on DD-sthings (not
// shared) can be cloned to another node.
//
// Every step is idempotent: a template already on the target node is not
// migrated, a copy that already exists by name is reported, not cloned again.
package promote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/repin"
)

// LabUL defaults (pve-fs9100-copy.py, #3103, #4652).
const (
	DefaultTarget      = "ul-pve11"
	DefaultCopyNode    = "ul-pve01"
	DefaultCopyStorage = "FS9100-01-1"
	DefaultCopyPool    = "stuttgart-things"
	DefaultCopySuffix  = "-fs9100"
)

// Options of a promotion.
type Options struct {
	SourceID      int    `json:"source_id"`
	To            string `json:"to,omitempty"` // target node of the template ("" = do not migrate)
	TargetStorage string `json:"target_storage,omitempty"`
	Copy          bool   `json:"fs9100_copy"`
	CopyNode      string `json:"copy_node,omitempty"`
	CopyStorage   string `json:"copy_storage,omitempty"`
	CopyPool      string `json:"copy_pool,omitempty"`
	CopySuffix    string `json:"copy_suffix,omitempty"`
	OS            string `json:"os,omitempty"` // repin key; "" = from the template name
	SkipChecks    bool   `json:"skip_checks,omitempty"`
}

func (o *Options) defaults() {
	if o.CopyNode == "" {
		o.CopyNode = DefaultCopyNode
	}
	if o.CopyStorage == "" {
		o.CopyStorage = DefaultCopyStorage
	}
	if o.CopyPool == "" {
		o.CopyPool = DefaultCopyPool
	}
	if o.CopySuffix == "" {
		o.CopySuffix = DefaultCopySuffix
	}
}

// Step states.
const (
	StatePlanned = "planned"
	StateSkipped = "skipped" // nothing to do (idempotent)
	StateDone    = "done"
	StateFailed  = "failed"
)

// Step is one step of the plan.
type Step struct {
	ID     string        `json:"id"` // migrate, copy-clone, copy-template
	Title  string        `json:"title"`
	State  string        `json:"state"`
	Reason string        `json:"reason,omitempty"`
	Calls  []pve.APICall `json:"calls,omitempty"`
	UPID   string        `json:"upid,omitempty"`
}

// Guest is a VM/template the plan touches.
type Guest struct {
	VMID     int        `json:"vmid"`
	Name     string     `json:"name"`
	Node     string     `json:"node"`
	Template bool       `json:"template"`
	Disks    []pve.Disk `json:"disks,omitempty"`
	Exists   bool       `json:"exists"`
}

// Plan is what a promotion will do (or did, after Execute).
type Plan struct {
	Options Options        `json:"options"`
	Source  Guest          `json:"source"`
	Checks  []pve.Check    `json:"checks"`
	Copy    *Guest         `json:"copy,omitempty"`
	Steps   []Step         `json:"steps"`
	Repins  []repin.Change `json:"repins"`
	Notes   []string       `json:"notes,omitempty"`
}

// Pending reports whether any step still has work to do.
func (p *Plan) Pending() bool {
	for _, s := range p.Steps {
		if s.State == StatePlanned {
			return true
		}
	}
	return false
}

// ErrNotTemplate: the source is a VM, not a template.
var ErrNotTemplate = errors.New("not a template")

// ErrChecks: the source config failed a sanity check.
var ErrChecks = errors.New("config checks failed")

// API is the part of *pve.Client a promotion needs (tests fake it).
type API interface {
	Resources(ctx context.Context) ([]pve.Resource, error)
	Config(ctx context.Context, node string, vmid int) (pve.Config, error)
	NextID(ctx context.Context) (int, error)
	IDFree(ctx context.Context, vmid int) bool
	MigrateCheck(ctx context.Context, node string, vmid int, target string) (pve.MigrateCheck, error)
	Migrate(ctx context.Context, r pve.MigrateRequest) (string, error)
	Clone(ctx context.Context, r pve.CloneRequest) (string, error)
	ConvertToTemplate(ctx context.Context, node string, vmid int) (string, error)
	WaitTask(ctx context.Context, upid string, opts pve.WaitOptions) (pve.TaskStatus, error)
}

// Build reads the cluster and returns the plan. It writes nothing. A plan
// is returned together with ErrNotTemplate/ErrChecks so callers can show it.
func Build(ctx context.Context, api API, o Options) (*Plan, error) {
	o.defaults()
	rs, err := api.Resources(ctx)
	if err != nil {
		return nil, err
	}
	src, err := pve.FindIn(rs, o.SourceID)
	if err != nil {
		return nil, err
	}
	cfg, err := api.Config(ctx, src.Node, src.VMID)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Options: o,
		Source: Guest{VMID: src.VMID, Name: src.Name, Node: src.Node, Template: cfg.IsTemplate(),
			Disks: cfg.Disks(), Exists: true},
		Checks: pve.CheckConfig(cfg),
	}
	if p.Options.OS == "" {
		p.Options.OS = repin.DetectOS(src.Name)
	}
	if !p.Source.Template {
		return p, fmt.Errorf("VMID %d (%s) on %s: %w", src.VMID, src.Name, src.Node, ErrNotTemplate)
	}

	// 1. the template itself to the target node
	if o.To != "" {
		if src.Node == o.To {
			p.Steps = append(p.Steps, Step{ID: "migrate", Title: "migrate the template to " + o.To, State: StateSkipped,
				Reason: fmt.Sprintf("%d is already on %s", src.VMID, o.To)})
		} else {
			mc, err := api.MigrateCheck(ctx, src.Node, src.VMID, o.To)
			if err != nil {
				return nil, err
			}
			if b := mc.Blocked(o.To); b != nil && !(len(mc.UnavailableStorages(o.To)) > 0 && o.TargetStorage != "") {
				return p, fmt.Errorf("%s not allowed for %d: %v (pass --target-storage <storage on %s> if storages are missing)", o.To, src.VMID, b, o.To)
			}
			mr := pve.MigrateRequest{Node: src.Node, VMID: src.VMID, Target: o.To, TargetStorage: o.TargetStorage,
				WithLocalDisks: len(mc.LocalDisks) > 0}
			p.Steps = append(p.Steps, Step{ID: "migrate", Title: fmt.Sprintf("migrate %d %s -> %s (offline)", src.VMID, src.Node, o.To),
				State: StatePlanned, Calls: []pve.APICall{mr.Call()}})
		}
	}

	// 2. the copy onto shared storage, cloned from the template's node after step 1
	cloneNode := src.Node
	if o.To != "" {
		cloneNode = o.To
	}
	copyID := 0
	if o.Copy {
		name := src.Name + o.CopySuffix
		if existing := pve.FindByName(rs, name); len(existing) > 0 {
			e := existing[0]
			p.Copy = &Guest{VMID: e.VMID, Name: e.Name, Node: e.Node, Template: bool(e.Template), Exists: true}
			if ecfg, err := api.Config(ctx, e.Node, e.VMID); err == nil {
				p.Copy.Disks = ecfg.Disks()
			}
			reason := fmt.Sprintf("%s already exists as VMID %d on %s", name, e.VMID, e.Node)
			p.Steps = append(p.Steps, Step{ID: "copy-clone", Title: "full copy to " + o.CopyStorage, State: StateSkipped, Reason: reason})
			if !p.Copy.Template {
				p.Steps = append(p.Steps, Step{ID: "copy-template", Title: "convert the copy to a template", State: StatePlanned,
					Calls: []pve.APICall{pve.TemplateCall(e.Node, e.VMID)}})
			}
			if len(existing) > 1 {
				p.Notes = append(p.Notes, fmt.Sprintf("%d guests are named %s; using VMID %d", len(existing), name, e.VMID))
			}
			copyID = e.VMID
		} else {
			newID, err := api.NextID(ctx)
			if err != nil {
				return nil, err
			}
			copyID = newID
			p.Copy = &Guest{VMID: newID, Name: name, Node: o.CopyNode, Template: true}
			clone := cloneRequest(cloneNode, p.Source, o, newID)
			p.Steps = append(p.Steps, Step{ID: "copy-clone", Title: fmt.Sprintf("full clone %d (%s) -> %d %s on %s/%s", src.VMID, cloneNode, newID, name, o.CopyNode, o.CopyStorage),
				State: StatePlanned, Calls: []pve.APICall{clone.Call()}})
			p.Steps = append(p.Steps, Step{ID: "copy-template", Title: fmt.Sprintf("convert %d to a template", newID),
				State: StatePlanned, Calls: []pve.APICall{pve.TemplateCall(o.CopyNode, newID)}})
			p.Notes = append(p.Notes, fmt.Sprintf("VMID %d is the next free ID now; if it is taken by the time the clone runs, the next free one is used", newID))
		}
	}

	p.Repins = repin.ForPromote(p.Options.OS, src.VMID, copyID)

	if !o.SkipChecks && !pve.ChecksOK(p.Checks) {
		return p, fmt.Errorf("VMID %d (%s): %w", src.VMID, src.Name, ErrChecks)
	}
	return p, nil
}

const copyDescription = "Copy of template %d (%s) with its disk on %s, so Telmate clones (create-terraform-vm) can allocate (DD-sthings: 403). Made by pvectl template promote."

// cloneRequest is the clone of pve-fs9100-copy.py: full, onto the shared
// storage, target node = copy node, from node (where the template is).
func cloneRequest(node string, src Guest, o Options, newID int) pve.CloneRequest {
	return pve.CloneRequest{
		Node: node, VMID: src.VMID, NewID: newID, Name: src.Name + o.CopySuffix,
		Full: true, Storage: o.CopyStorage, Target: o.CopyNode, Pool: o.CopyPool,
		Description: fmt.Sprintf(copyDescription, src.VMID, src.Name, o.CopyStorage),
	}
}

// Progress receives what Execute does.
type Progress interface {
	StepStarted(s Step)
	TaskStarted(s Step, upid string)
	TaskEvent(s Step, ev pve.TaskEvent)
	StepDone(s Step)
}

// Execute runs the planned steps of p in order and updates their state.
// It stops at the first failure.
func Execute(ctx context.Context, api API, p *Plan, prog Progress) error {
	o := p.Options
	o.defaults()
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.State != StatePlanned {
			continue
		}
		prog.StepStarted(*s)
		upid, err := runStep(ctx, api, p, o, s)
		if err == nil && upid != "" {
			s.UPID = upid
			prog.TaskStarted(*s, upid)
			_, err = api.WaitTask(ctx, upid, pve.WaitOptions{OnEvent: func(ev pve.TaskEvent) { prog.TaskEvent(*s, ev) }})
		}
		if err != nil {
			s.State = StateFailed
			s.Reason = err.Error()
			prog.StepDone(*s)
			return fmt.Errorf("step %s: %w", s.ID, err)
		}
		s.State = StateDone
		if s.ID == "migrate" {
			p.Source.Node = o.To // the copy clones from here
		}
		prog.StepDone(*s)
	}
	return nil
}

func runStep(ctx context.Context, api API, p *Plan, o Options, s *Step) (string, error) {
	switch s.ID {
	case "copy-clone":
		newID := p.Copy.VMID
		if !api.IDFree(ctx, newID) {
			id, err := api.NextID(ctx)
			if err != nil {
				return "", err
			}
			old := p.Copy.VMID
			newID = id
			p.Copy.VMID = id
			p.Repins = repin.ForPromote(p.Options.OS, p.Source.VMID, id)
			p.Notes = append(p.Notes, fmt.Sprintf("planned VMID %d was taken meanwhile; the copy got %d", old, id))
			// later steps name the planned ID
			for j := range p.Steps {
				p.Steps[j].Title = strings.ReplaceAll(p.Steps[j].Title, fmt.Sprint(old), fmt.Sprint(id))
				for k := range p.Steps[j].Calls {
					p.Steps[j].Calls[k].Path = strings.ReplaceAll(p.Steps[j].Calls[k].Path, fmt.Sprintf("/%d/", old), fmt.Sprintf("/%d/", id))
				}
			}
		}
		req := cloneRequest(p.Source.Node, p.Source, o, newID)
		s.Calls = []pve.APICall{req.Call()}
		return api.Clone(ctx, req)
	case "copy-template":
		node := p.Copy.Node
		upid, err := api.ConvertToTemplate(ctx, node, p.Copy.VMID)
		if err == nil && upid == "" {
			time.Sleep(3 * time.Second) // older PVE: synchronous, settle like pve-fs9100-copy.py
		}
		return upid, err
	case "migrate":
		mc, err := api.MigrateCheck(ctx, p.Source.Node, p.Source.VMID, o.To)
		if err != nil {
			return "", err
		}
		return api.Migrate(ctx, pve.MigrateRequest{Node: p.Source.Node, VMID: p.Source.VMID, Target: o.To,
			TargetStorage: o.TargetStorage, WithLocalDisks: len(mc.LocalDisks) > 0})
	}
	return "", fmt.Errorf("unknown step %q", s.ID)
}
