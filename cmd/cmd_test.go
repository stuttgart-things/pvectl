package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/pve/pvetest"
)

const secretNotes = "root password: hunter2-PLAINTEXT"

func fakeLab(t *testing.T) *pvetest.Server {
	s := pvetest.New(t)
	s.Data("GET /cluster/resources", []map[string]any{
		{"vmid": 280, "name": "ubuntu26-base-20261116-0842", "node": "ul-pve01", "type": "qemu", "status": "stopped", "template": 1},
		{"vmid": 279, "name": "rocky10-base-20261115-2011", "node": "ul-pve11", "type": "qemu", "status": "stopped", "template": 1},
		{"vmid": 266, "name": "ubuntu26-base-20261002-0758", "node": "ul-pve11", "type": "qemu", "status": "stopped", "template": 1},
		{"vmid": 227, "name": "platform-sthings-1", "node": "ul-pve10", "type": "qemu", "status": "running", "template": 0, "uptime": 7260},
	})
	s.Data("GET /nodes/ul-pve01/qemu/280/config", map[string]any{
		"name": "ubuntu26-base-20261116-0842", "template": 1, "cpu": "host", "agent": "1",
		"virtio0": "DD-sthings:base-280-disk-0,size=32G", "ide2": "DD-sthings:vm-280-cloudinit,media=cdrom",
		"description": secretNotes,
	})
	s.Data("GET /nodes/ul-pve11/qemu/266/config", map[string]any{
		"name": "ubuntu26-base-20261002-0758", "template": 1, "cpu": "host", "agent": "1",
		"virtio0": "DD-sthings:base-266-disk-0", "description": "user: hunter2"})
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
	t.Setenv("PVE_KNOWN_HOSTS", filepath.Join(t.TempDir(), "known_hosts"))
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
	t.Setenv("PVE_API_TOKEN_ID", "")
	t.Setenv("PVE_API_TOKEN_SECRET", "")
	out, _, code := run(t, "vms", "-o", "json")
	if code != ExitUsage || !strings.Contains(decode(t, out)["error"].(string), "sops exec-env") {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestAPITokenFromEnv(t *testing.T) {
	fakeLab(t)
	t.Setenv("PVE_API_USER", "")
	t.Setenv("PVE_API_PASSWORD", "")
	t.Setenv("PVE_API_TOKEN_ID", pvetest.TokenID)
	t.Setenv("PVE_API_TOKEN_SECRET", pvetest.TokenSecret)
	out, _, code := run(t, "vms", "--templates", "-o", "json")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("exit %d %s", code, out)
	}
}

func TestUnknownFlagIsUsage(t *testing.T) {
	_, _, code := run(t, "vms", "--nope")
	if code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestPromoteOlderNeedsAllowOld(t *testing.T) {
	s := fakeLab(t)
	out, _, code := run(t, "template", "promote", "266", "--fs9100-copy", "--yes", "-o", "json")
	if code != ExitCheckFailed {
		t.Fatalf("exit %d %s", code, out)
	}
	m := decode(t, out)
	if ws := m["plan"].(map[string]any)["warnings"].([]any); len(ws) != 1 || !strings.Contains(m["error"].(string), "--allow-old") {
		t.Fatalf("%v", m)
	}
	if w := s.Writes(); len(w) != 0 {
		t.Fatalf("wrote: %+v", w)
	}
	// the dry-run shows the warning and exits 0
	out, _, code = run(t, "template", "promote", "266", "--fs9100-copy", "--dry-run", "-o", "json")
	if code != ExitOK || !strings.Contains(out, "is not the newest") || strings.Contains(out, "hunter2") {
		t.Fatalf("dry-run exit %d %s", code, out)
	}
}

func TestVMsFindsTemplateByVMID(t *testing.T) {
	fakeLab(t)
	out, _, code := run(t, "vms", "280", "-o", "json")
	if code != ExitOK || !strings.Contains(out, "ubuntu26-base-20261116-0842") {
		t.Fatalf("exit %d %s", code, out)
	}
}

// The terminal look (cards, tables, banner) must not leak descriptions either.
func TestFancyOutputHasNoDescription(t *testing.T) {
	fakeLab(t)
	fancyOverride = true
	t.Cleanup(func() { fancyOverride = false })
	for _, args := range [][]string{
		{"config", "280"},
		{"config", "266"},
		{"vms", "--all"},
		{"vms", "266"},
		{"template", "promote", "280", "--to", "ul-pve11", "--fs9100-copy", "--dry-run"},
		{"template", "promote", "266", "--fs9100-copy", "--dry-run"},
	} {
		out, errOut, code := run(t, args...)
		if code != ExitOK {
			t.Fatalf("%v: exit %d\n%s%s", args, code, out, errOut)
		}
		if strings.Contains(out+errOut, "hunter2") || strings.Contains(out+errOut, "description:") {
			t.Fatalf("%v leaks the description:\n%s", args, out)
		}
		if !strings.Contains(out, "╭") {
			t.Fatalf("%v: no card or table in the fancy output:\n%s", args, out)
		}
	}
	out, _, _ := run(t, "template", "promote", "266", "--fs9100-copy", "--dry-run")
	if !strings.Contains(out, "WARNINGS") || !strings.Contains(out, "is not the newest") {
		t.Fatalf("promote card without warnings:\n%s", out)
	}
}

func TestRootWithoutTTYPrintsHelp(t *testing.T) {
	out, _, code := run(t)
	if code != ExitOK || !strings.Contains(out, "Usage:") || strings.Contains(out, "█") {
		t.Fatalf("exit %d, want the plain help:\n%s", code, out)
	}
	if _, _, code := run(t, "nosuchcommand"); code != ExitUsage {
		t.Fatalf("unknown command: exit %d, want %d", code, ExitUsage)
	}
}

func TestTLSTrust(t *testing.T) {
	s := fakeLab(t)
	fp := pve.Fingerprint(s.Certificate())

	out, _, code := run(t, "tls", "-o", "json")
	if code != ExitOK || decode(t, out)["trust"] != "untrusted" || decode(t, out)["fingerprint"] != fp {
		t.Fatalf("tls: exit %d\n%s", code, out)
	}
	if _, _, code := run(t, "tls", "trust"); code != ExitUsage {
		t.Fatalf("trust without --fingerprint/--yes: exit %d, want %d", code, ExitUsage)
	}
	if _, _, code := run(t, "tls", "trust", "--fingerprint", strings.Repeat("00", 32)); code != ExitCheckFailed {
		t.Fatalf("trust with a wrong fingerprint: exit %d, want %d", code, ExitCheckFailed)
	}
	out, _, code = run(t, "tls", "trust", "--fingerprint", fp, "-o", "json")
	if code != ExitOK || decode(t, out)["pinned"] != true {
		t.Fatalf("trust: exit %d\n%s", code, out)
	}
	out, _, _ = run(t, "tls", "-o", "json")
	if decode(t, out)["trust"] != "pinned" {
		t.Fatalf("after trust:\n%s", out)
	}
}
