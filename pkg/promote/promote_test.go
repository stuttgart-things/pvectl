package promote

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stuttgart-things/pvectl/pkg/pve"
)

type fakeAPI struct {
	rs      []pve.Resource
	cfgs    map[int]pve.Config
	next    int
	taken   map[int]bool
	writes  []string
	blocked map[string]map[string]any
}

func (f *fakeAPI) Resources(context.Context) ([]pve.Resource, error) { return f.rs, nil }
func (f *fakeAPI) Config(_ context.Context, _ string, id int) (pve.Config, error) {
	if c, ok := f.cfgs[id]; ok {
		return c, nil
	}
	return nil, pve.ErrNotFound
}
func (f *fakeAPI) NextID(context.Context) (int, error) {
	for f.taken[f.next] {
		f.next++
	}
	return f.next, nil
}
func (f *fakeAPI) IDFree(_ context.Context, id int) bool { return !f.taken[id] }
func (f *fakeAPI) MigrateCheck(_ context.Context, _ string, _ int, _ string) (pve.MigrateCheck, error) {
	return pve.MigrateCheck{LocalDisks: []string{"DD-sthings:base-280-disk-0"}, NotAllowedNodes: f.blocked}, nil
}
func (f *fakeAPI) Migrate(_ context.Context, r pve.MigrateRequest) (string, error) {
	f.writes = append(f.writes, fmt.Sprintf("migrate %d %s->%s disks=%v", r.VMID, r.Node, r.Target, r.WithLocalDisks))
	return "UPID:" + r.Node + ":1:1:1:qmigrate:" + fmt.Sprint(r.VMID) + ":u@pve:", nil
}
func (f *fakeAPI) Clone(_ context.Context, r pve.CloneRequest) (string, error) {
	f.writes = append(f.writes, fmt.Sprintf("clone %d->%d %s on %s storage=%s target=%q", r.VMID, r.NewID, r.Name, r.Node, r.Storage, r.Target))
	return "UPID:" + r.Node + ":1:1:1:qmclone:" + fmt.Sprint(r.VMID) + ":u@pve:", nil
}
func (f *fakeAPI) ConvertToTemplate(_ context.Context, node string, id int) (string, error) {
	f.writes = append(f.writes, fmt.Sprintf("template %d on %s", id, node))
	return "UPID:" + node + ":1:1:1:qmtemplate:" + fmt.Sprint(id) + ":u@pve:", nil
}
func (f *fakeAPI) WaitTask(context.Context, string, pve.WaitOptions) (pve.TaskStatus, error) {
	return pve.TaskStatus{Status: "stopped", ExitStatus: "OK"}, nil
}

type nopProgress struct{}

func (nopProgress) StepStarted(Step)              {}
func (nopProgress) TaskStarted(Step, string)      {}
func (nopProgress) TaskEvent(Step, pve.TaskEvent) {}
func (nopProgress) StepDone(Step)                 {}

const srcName = "ubuntu26-base-20261116-0842"

func goodCfg() pve.Config {
	return pve.Config{"name": srcName, "template": "1", "cpu": "host", "agent": "1",
		"virtio0": "DD-sthings:base-280-disk-0,size=32G", "ide2": "DD-sthings:vm-280-cloudinit,media=cdrom"}
}

func fresh() *fakeAPI {
	return &fakeAPI{
		rs:   []pve.Resource{{VMID: 280, Name: srcName, Node: "ul-pve01", Template: true}},
		cfgs: map[int]pve.Config{280: goodCfg()},
		next: 283, taken: map[int]bool{},
	}
}

func TestFreshPromotion(t *testing.T) {
	api := fresh()
	p, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range p.Steps {
		ids = append(ids, s.ID+":"+s.State)
	}
	want := "[migrate:planned copy-clone:planned copy-template:planned]"
	if fmt.Sprint(ids) != want {
		t.Fatalf("steps %v, want %s", ids, want)
	}
	if p.Copy.VMID != 283 || p.Copy.Name != srcName+"-fs9100" {
		t.Fatalf("copy %+v", p.Copy)
	}
	if len(api.writes) != 0 {
		t.Fatalf("Build wrote: %v", api.writes)
	}
	if err := Execute(context.Background(), api, p, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	wantWrites := []string{
		"migrate 280 ul-pve01->ul-pve11 disks=true",
		`clone 280->283 ` + srcName + `-fs9100 on ul-pve11 storage=FS9100-01-1 target="ul-pve01"`,
		"template 283 on ul-pve01",
	}
	if fmt.Sprint(api.writes) != fmt.Sprint(wantWrites) {
		t.Fatalf("writes\n%v\nwant\n%v", api.writes, wantWrites)
	}
	// repins: ubuntu26 -> two values.yaml keys + two vm_clone_id
	if len(p.Repins) != 4 || p.Repins[0].Value != "280" || p.Repins[3].Value != "283" {
		t.Fatalf("repins %+v", p.Repins)
	}
}

func TestIdempotent(t *testing.T) {
	api := fresh()
	api.rs = []pve.Resource{
		{VMID: 280, Name: srcName, Node: "ul-pve11", Template: true},
		{VMID: 282, Name: srcName + "-fs9100", Node: "ul-pve01", Template: true},
	}
	api.cfgs[282] = pve.Config{"virtio0": "FS9100-01-1:base-282-disk-0"}
	p, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Pending() {
		t.Fatalf("expected nothing to do: %+v", p.Steps)
	}
	if p.Copy.VMID != 282 || p.Repins[len(p.Repins)-1].Value != "282" {
		t.Fatalf("copy %+v repins %+v", p.Copy, p.Repins)
	}
}

func TestCopyOnlyFromTargetNode(t *testing.T) {
	api := fresh()
	api.rs[0].Node = "ul-pve11"
	p, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(context.Background(), api, p, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`clone 280->283 ` + srcName + `-fs9100 on ul-pve11 storage=FS9100-01-1 target="ul-pve01"`,
		"template 283 on ul-pve01",
	}
	if fmt.Sprint(api.writes) != fmt.Sprint(want) {
		t.Fatalf("writes %v", api.writes)
	}
}

func TestPlannedIDTaken(t *testing.T) {
	api := fresh()
	p, err := Build(context.Background(), api, Options{SourceID: 280, Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	api.taken[283] = true
	if err := Execute(context.Background(), api, p, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	if p.Copy.VMID != 284 || api.writes[1] != "template 284 on ul-pve01" || p.Repins[len(p.Repins)-1].Value != "284" {
		t.Fatalf("copy %d writes %v", p.Copy.VMID, api.writes)
	}
}

func TestNotATemplate(t *testing.T) {
	api := fresh()
	api.cfgs[280] = pve.Config{"name": srcName}
	_, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11"})
	if !errors.Is(err, ErrNotTemplate) {
		t.Fatalf("got %v", err)
	}
}

func TestChecksFail(t *testing.T) {
	api := fresh()
	cfg := goodCfg()
	cfg["cpu"] = "x86-64-v2-AES"
	api.cfgs[280] = cfg
	p, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11"})
	if !errors.Is(err, ErrChecks) || p == nil {
		t.Fatalf("got %v", err)
	}
	if _, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11", SkipChecks: true}); err != nil {
		t.Fatalf("skip-checks: %v", err)
	}
}

func TestBlockedTarget(t *testing.T) {
	api := fresh()
	api.blocked = map[string]map[string]any{"ul-pve11": {"unavailable_storages": []any{"DD-sthings"}}}
	if _, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11"}); err == nil {
		t.Fatal("want error without --target-storage")
	}
	if _, err := Build(context.Background(), api, Options{SourceID: 280, To: "ul-pve11", TargetStorage: "local-lvm"}); err != nil {
		t.Fatalf("with target storage: %v", err)
	}
}
