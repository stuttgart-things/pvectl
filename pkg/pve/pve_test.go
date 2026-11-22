package pve_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/pve/pvetest"
)

const notes = "root password: hunter2-PLAINTEXT"

func client(t *testing.T, s *pvetest.Server) *pve.Client {
	t.Helper()
	c, err := pve.New(pve.Options{URL: s.APIURL(), User: "test@pve", Password: s.Password, Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func templateConfig() map[string]any {
	return map[string]any{
		"name": "ubuntu26-base-20261116-0842", "template": 1, "cpu": "host", "agent": "1",
		"virtio0": "DD-sthings:base-280-disk-0,size=32G", "ide2": "DD-sthings:vm-280-cloudinit,media=cdrom",
		"description": notes, "cipassword": "$6$hash", "cores": 4,
	}
}

func TestConfigNeverReturnsDescription(t *testing.T) {
	s := pvetest.New(t)
	s.Data("GET /nodes/ul-pve11/qemu/280/config", templateConfig())
	cfg, err := client(t, s).Config(context.Background(), "ul-pve11", 280)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "description") {
		t.Fatalf("description leaked: %s", b)
	}
	if cfg["cipassword"] != pve.Masked {
		t.Errorf("cipassword = %q, want masked", cfg["cipassword"])
	}
	if cfg["cores"] != "4" || !cfg.IsTemplate() {
		t.Errorf("cores=%q template=%v", cfg["cores"], cfg.IsTemplate())
	}
}

func TestSanitizeDropsDescriptionAnyCase(t *testing.T) {
	cfg := pve.Sanitize(map[string]any{"Description": notes, "description": notes, "name": "x"})
	if len(cfg) != 1 {
		t.Fatalf("got %v", cfg)
	}
}

func TestCheckConfig(t *testing.T) {
	cfg := pve.Sanitize(templateConfig())
	if checks := pve.CheckConfig(cfg); !pve.ChecksOK(checks) {
		t.Fatalf("good template failed: %+v", checks)
	}
	cases := map[string]map[string]any{
		"cpu":              {"cpu": "x86-64-v2-AES", "agent": "1", "ide2": "local:vm-1-cloudinit,media=cdrom"},
		"agent":            {"cpu": "host", "agent": "0", "ide2": "local:vm-1-cloudinit,media=cdrom"},
		"cloud-init drive": {"cpu": "host,flags=+aes", "agent": "enabled=1,fstrim_cloned_disks=1", "ide2": "none,media=cdrom"},
	}
	// a template without a cloud-init drive passes (LabUL: bpg/Telmate add one per clone)
	tpl := pve.CheckConfig(pve.Sanitize(map[string]any{"template": 1, "cpu": "host", "agent": "1", "virtio0": "DD-sthings:base-280-disk-0"}))
	if !pve.ChecksOK(tpl) || !tpl[2].OK {
		t.Errorf("template without cloud-init drive: %+v", tpl)
	}
	for want, raw := range cases {
		checks := pve.CheckConfig(pve.Sanitize(raw))
		for _, c := range checks {
			if (c.Name == want) == c.OK {
				t.Errorf("%s: check %s ok=%v (%s)", want, c.Name, c.OK, c.Detail)
			}
		}
	}
	if pve.ChecksOK(pve.CheckConfig(pve.Config{})) {
		t.Error("empty config passed")
	}
}

func TestCheckCPU(t *testing.T) {
	cases := []struct {
		cpu, ostype string
		ok, warn    bool
	}{
		{"host", "l26", true, false},
		{"x86-64-v3", "l26", true, false}, // Deb13 (996)
		{"x86-64-v4,flags=+aes", "", true, false},
		{"cputype=host", "", true, false},
		{"x86-64-v2-AES", "l26", false, false}, // EL10 would not boot
		{"x86-64-v2-AES", "", false, false},    // unset ostype counts as Linux
		{"", "l26", false, false},
		{"x86-64-v2-AES", "win11", false, true}, // Windows Server 2025 (999)
		{"x86-64-v2-AES", "win10", false, true},
		{"x86-64-v2-AES", "other", false, true}, // DDVE (213)
		{"", "win11", false, true},
	}
	for _, c := range cases {
		cfg := pve.Config{"cpu": c.cpu, "ostype": c.ostype, "agent": "1", "template": "1"}
		got := pve.CheckConfig(cfg)[0]
		if got.Name != "cpu" || got.OK != c.ok || got.Warn != c.warn {
			t.Errorf("cpu %q ostype %q: ok=%v warn=%v (%s), want ok=%v warn=%v",
				c.cpu, c.ostype, got.OK, got.Warn, got.Detail, c.ok, c.warn)
		}
		if pve.ChecksOK(pve.CheckConfig(cfg)) != (c.ok || c.warn) {
			t.Errorf("cpu %q ostype %q: ChecksOK wrong", c.cpu, c.ostype)
		}
	}
}

func TestDisks(t *testing.T) {
	ds := pve.Sanitize(templateConfig()).Disks()
	if len(ds) != 2 || ds[0].Key != "ide2" || !ds[0].CloudInit || ds[1].Storage != "DD-sthings" {
		t.Fatalf("%+v", ds)
	}
}

func TestParsePercent(t *testing.T) {
	for line, want := range map[string]float64{
		"transferred 3.2 GiB of 32.0 GiB (10.00%)":                  10,
		"drive-virtio0: transferred 32.0 GiB of 32.0 GiB (100.00%)": 100,
		"mirror-virtio0: 55% done":                                  55,
	} {
		got, ok := pve.ParsePercent(line)
		if !ok || got != want {
			t.Errorf("%q: %v %v", line, got, ok)
		}
	}
	if _, ok := pve.ParsePercent("create full clone of drive virtio0"); ok {
		t.Error("no percent expected")
	}
}

func TestParseUPID(t *testing.T) {
	u, err := pve.ParseUPID("UPID:ul-pve01:0012ABCD:0A1B2C3D:6738F2A0:qmclone:280:user@pve:")
	if err != nil || u.Node != "ul-pve01" || u.Type != "qmclone" || u.ID != "280" || u.StartTime != 0x6738F2A0 {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := pve.ParseUPID("nope"); err == nil {
		t.Error("want error")
	}
}

func TestFilter(t *testing.T) {
	rs := []pve.Resource{{VMID: 280, Name: "ubuntu26-base", Template: true}, {VMID: 227, Name: "platform-sthings-1"}}
	if got := pve.Filter(rs, "", pve.KindVMs); len(got) != 1 || got[0].VMID != 227 {
		t.Errorf("vms: %+v", got)
	}
	if got := pve.Filter(rs, "280", pve.KindAll); len(got) != 1 || got[0].VMID != 280 {
		t.Errorf("by id: %+v", got)
	}
	if got := pve.Filter(rs, "ubuntu", pve.KindTemplates); len(got) != 1 {
		t.Errorf("templates: %+v", got)
	}
}

func TestLoginFailureHidesPassword(t *testing.T) {
	s := pvetest.New(t)
	c, _ := pve.New(pve.Options{URL: s.APIURL(), User: "test@pve", Password: "wrong-pass-XYZ", Insecure: true})
	_, err := c.Resources(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "wrong-pass-XYZ") {
		t.Fatalf("password in error: %v", err)
	}
}

func TestNotFound(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET /nodes/ul-pve01/qemu/9/config", func(pvetest.Call) (any, int, string) {
		return nil, 500, "Configuration file 'nodes/ul-pve01/qemu-server/9.conf' does not exist"
	})
	_, err := client(t, s).Config(context.Background(), "ul-pve01", 9)
	if !errors.Is(err, pve.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestWaitTask(t *testing.T) {
	s := pvetest.New(t)
	upid := "UPID:ul-pve01:00000001:00000001:6738F2A0:qmclone:280:user@pve:"
	polls := 0
	s.Handle("GET /nodes/ul-pve01/tasks/"+upid+"/status", func(pvetest.Call) (any, int, string) {
		polls++
		if polls < 2 {
			return map[string]any{"status": "running", "type": "qmclone", "id": "280"}, 200, ""
		}
		return map[string]any{"status": "stopped", "exitstatus": "OK", "type": "qmclone", "id": "280"}, 200, ""
	})
	s.Handle("GET /nodes/ul-pve01/tasks/"+upid+"/log", func(c pvetest.Call) (any, int, string) {
		all := []map[string]any{{"n": 1, "t": "create full clone"}, {"n": 2, "t": "transferred 1 GiB of 2 GiB (50.00%)"}, {"n": 3, "t": "TASK OK"}}
		q, _ := url.ParseQuery(c.Query)
		start, _ := strconv.Atoi(q.Get("start"))
		if start > len(all) {
			start = len(all)
		}
		return all[start:], 200, ""
	})
	var lines []string
	var last float64
	st, err := client(t, s).WaitTask(context.Background(), upid, pve.WaitOptions{Poll: 1, OnEvent: func(ev pve.TaskEvent) {
		lines = append(lines, ev.Line)
		last = ev.Percent
	}})
	if err != nil || !st.OK() {
		t.Fatalf("%+v %v", st, err)
	}
	if len(lines) != 3 || last != 50 {
		t.Fatalf("lines=%v percent=%v", lines, last)
	}

	s.Handle("GET /nodes/ul-pve01/tasks/"+upid+"/status", func(pvetest.Call) (any, int, string) {
		return map[string]any{"status": "stopped", "exitstatus": "clone failed: 403", "type": "qmclone", "id": "280"}, 200, ""
	})
	if _, err := client(t, s).WaitTask(context.Background(), upid, pve.WaitOptions{Poll: 1}); !errors.Is(err, pve.ErrTaskFailed) {
		t.Fatalf("want ErrTaskFailed, got %v", err)
	}
}

func TestMigrateCheck(t *testing.T) {
	s := pvetest.New(t)
	s.Data("GET /nodes/ul-pve01/qemu/280/migrate", map[string]any{
		"running": 0, "local_disks": []map[string]any{{"volid": "DD-sthings:base-280-disk-0"}},
		"not_allowed_nodes": map[string]any{"ul-pve02": map[string]any{"unavailable_storages": []string{"DD-sthings"}}},
	})
	mc, err := client(t, s).MigrateCheck(context.Background(), "ul-pve01", 280, "ul-pve11")
	if err != nil {
		t.Fatal(err)
	}
	if mc.Running || len(mc.LocalDisks) != 1 || mc.Blocked("ul-pve11") != nil || mc.UnavailableStorages("ul-pve02")[0] != "DD-sthings" {
		t.Fatalf("%+v", mc)
	}
}

func TestCloneCallRedactsDescription(t *testing.T) {
	call := pve.CloneRequest{Node: "ul-pve01", VMID: 280, NewID: 283, Name: "x-fs9100", Full: true, Description: notes}.Call()
	if strings.Contains(call.String(), "hunter2") || call.Params["description"] != pve.Redacted {
		t.Fatalf("%s", call)
	}
}
