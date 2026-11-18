package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stuttgart-things/pvectl/pkg/pve/pvetest"
)

const secretNotes = "root password: hunter2-PLAINTEXT"

func fakeLab(t *testing.T) *pvetest.Server {
	s := pvetest.New(t)
	s.Data("GET /cluster/resources", []map[string]any{
		{"vmid": 280, "name": "ubuntu26-base-20261116-0842", "node": "ul-pve01", "type": "qemu", "status": "stopped", "template": 1},
		{"vmid": 279, "name": "rocky10-base-20261115-2011", "node": "ul-pve11", "type": "qemu", "status": "stopped", "template": 1},
		{"vmid": 227, "name": "platform-sthings-1", "node": "ul-pve10", "type": "qemu", "status": "running", "template": 0, "uptime": 7260},
	})
	s.Data("GET /nodes/ul-pve01/qemu/280/config", map[string]any{
		"name": "ubuntu26-base-20261116-0842", "template": 1, "cpu": "host", "agent": "1",
		"virtio0": "DD-sthings:base-280-disk-0,size=32G", "ide2": "DD-sthings:vm-280-cloudinit,media=cdrom",
		"description": secretNotes,
	})
	s.Data("GET /nodes/ul-pve11/qemu/279/config", map[string]any{
		"name": "rocky10-base-20261115-2011", "template": 1, "cpu": "x86-64-v2-AES", "agent": "1",
		"scsi0": "DD-sthings:base-279-disk-0,size=32G", "ide2": "DD-sthings:vm-279-cloudinit,media=cdrom",
		"description": secretNotes,
	})
	s.Data("GET /nodes/ul-pve01/qemu/280/migrate", map[string]any{
		"running": 0, "local_disks": []map[string]any{{"volid": "DD-sthings:base-280-disk-0"}},
	})
	s.Data("GET /cluster/nextid", "283")
	s.Data("GET /nodes/ul-pve10/qemu/227/agent/network-get-interfaces", map[string]any{"result": []map[string]any{
		{"name": "ens18", "ip-addresses": []map[string]any{{"ip-address-type": "ipv4", "ip-address": "10.31.103.201"}}},
	}})
	s.Data("GET /nodes/ul-pve10/tasks", []map[string]any{})
	t.Setenv("PVE_API_URL", s.APIURL())
	t.Setenv("PVE_API_USER", "test@pve")
	t.Setenv("PVE_API_PASSWORD", s.Password)
	return s
}

func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// run executes pvectl with args and returns stdout, stderr and the exit code.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	resetFlags(rootCmd)
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	var outBuf, errBuf bytes.Buffer
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(&outBuf, ro); done <- struct{}{} }()
	go func() { _, _ = io.Copy(&errBuf, re); done <- struct{}{} }()

	rootCmd.SetArgs(append(args, "--insecure", "--non-interactive"))
	err := rootCmd.ExecuteContext(context.Background())
	code := reportError(err)

	_ = wo.Close()
	_ = we.Close()
	<-done
	<-done
	os.Stdout, os.Stderr = oldOut, oldErr
	return outBuf.String(), errBuf.String(), code
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, s)
	}
	return m
}

func TestConfigJSONHasNoDescription(t *testing.T) {
	fakeLab(t)
	out, errOut, code := run(t, "config", "280", "-o", "json")
	if code != ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "hunter2") || strings.Contains(out, "description") {
		t.Fatalf("description leaked:\n%s", out)
	}
	m := decode(t, out)
	if m["checks_ok"] != true {
		t.Fatalf("checks_ok: %v", m["checks_ok"])
	}
}

func TestConfigTextHasNoDescription(t *testing.T) {
	fakeLab(t)
	out, errOut, _ := run(t, "config", "279")
	if strings.Contains(out+errOut, "hunter2") || strings.Contains(out, "description") {
		t.Fatalf("description leaked:\n%s", out)
	}
}

func TestConfigCheckFailedExit4(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "config", "279", "-o", "json")
	if code != ExitCheckFailed {
		t.Fatalf("exit %d", code)
	}
	if m := decode(t, out); m["checks_ok"] != false {
		t.Fatalf("%v", m)
	}
}

func TestMissingArgumentNamesFlag(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "config", "-o", "json")
	if code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
	m := decode(t, out)
	if m["flag"] != "--vmid" || m["code"].(float64) != ExitUsage || m["kind"] != "usage" {
		t.Fatalf("%v", m)
	}
}

func TestNotFoundExit3(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "config", "999", "-o", "json")
	if code != ExitNotFound || decode(t, out)["kind"] != "not_found" {
		t.Fatalf("exit %d %s", code, out)
	}
	out, _, code = run(t, "vms", "nomatch", "-o", "json")
	if code != ExitNotFound || strings.TrimSpace(out) != "[]" {
		t.Fatalf("vms: exit %d %q", code, out)
	}
}

func TestMigrateNeedsYes(t *testing.T) {
	s := fakeLab(t)
	out, _, code := run(t, "migrate", "--vmid", "280", "--target", "ul-pve11", "-o", "json")
	if code != ExitUsage || decode(t, out)["flag"] != "--yes" {
		t.Fatalf("exit %d %s", code, out)
	}
	if w := s.Writes(); len(w) != 0 {
		t.Fatalf("wrote: %+v", w)
	}
}

func TestMigrateDryRun(t *testing.T) {
	s := fakeLab(t)
	out, _, code := run(t, "migrate", "ul-pve01", "280", "ul-pve11", "--dry-run", "-o", "json")
	if code != ExitOK {
		t.Fatalf("exit %d %s", code, out)
	}
	m := decode(t, out)
	req := m["request"].(map[string]any)
	if m["state"] != "planned" || req["path"] != "/nodes/ul-pve01/qemu/280/migrate" {
		t.Fatalf("%v", m)
	}
	if req["params"].(map[string]any)["with-local-disks"] != "1" {
		t.Fatalf("params %v", req["params"])
	}
	if w := s.Writes(); len(w) != 0 {
		t.Fatalf("wrote: %+v", w)
	}
}

func TestMigrateAlreadyThere(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "migrate", "--vmid", "279", "--target", "ul-pve11", "-o", "json")
	if code != ExitOK || decode(t, out)["state"] != "skipped" {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestPromoteDryRun(t *testing.T) {
	s := fakeLab(t)
	out, errOut, code := run(t, "template", "promote", "280", "--to", "ul-pve11", "--fs9100-copy", "--dry-run", "-o", "json")
	if code != ExitOK {
		t.Fatalf("exit %d %s %s", code, out, errOut)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatal("description leaked")
	}
	m := decode(t, out)
	plan := m["plan"].(map[string]any)
	if plan["copy"].(map[string]any)["vmid"].(float64) != 283 || len(plan["steps"].([]any)) != 3 {
		t.Fatalf("%v", plan)
	}
	if w := s.Writes(); len(w) != 0 {
		t.Fatalf("wrote: %+v", w)
	}
}

func TestPromoteChecksFail(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "template", "promote", "279", "--to", "ul-pve11", "--dry-run", "-o", "json")
	if code != ExitCheckFailed || decode(t, out)["error"] == nil {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestPromoteNeedsAction(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "template", "promote", "280", "-o", "json")
	if code != ExitUsage || decode(t, out)["flag"] != "--to" {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestVMsDetails(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "vms", "platform", "-o", "json")
	if code != ExitOK {
		t.Fatalf("exit %d %s", code, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	if ips := rows[0]["ips"].([]any); ips[0] != "10.31.103.201" {
		t.Fatalf("%v", rows[0])
	}
}

func TestMissingEnvIsUsage(t *testing.T) {
	t.Setenv("PVE_API_URL", "")
	t.Setenv("PVE_API_USER", "")
	t.Setenv("PVE_API_PASSWORD", "")
	out, _, code := run(t, "vms", "-o", "json")
	if code != ExitUsage || !strings.Contains(decode(t, out)["error"].(string), "sops exec-env") {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestUnknownFlagIsUsage(t *testing.T) {
	_, _, code := run(t, "vms", "--nope")
	if code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}
