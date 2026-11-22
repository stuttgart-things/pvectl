package pve_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stuttgart-things/pvectl/pkg/pve"
	"github.com/stuttgart-things/pvectl/pkg/pve/pvetest"
)

func TestFindByName(t *testing.T) {
	s := pvetest.New(t)
	s.Data("GET /cluster/resources", []map[string]any{
		{"vmid": 280, "name": "ubuntu26-base", "node": "ul-pve11", "type": "qemu", "template": 1, "pool": "LabUL-Template"},
		{"vmid": 281, "name": "ubuntu26-base", "node": "ul-pve01", "type": "qemu", "template": 1, "pool": "other"},
		{"vmid": 300, "name": "rocky10-base", "node": "ul-pve11", "type": "qemu", "template": 1},
	})
	c := client(t, s)
	ctx := context.Background()
	if r, err := c.FindByName(ctx, "rocky10-base", "", ""); err != nil || r.VMID != 300 {
		t.Fatalf("unique: %+v %v", r, err)
	}
	if r, err := c.FindByName(ctx, "ubuntu26-base", "LabUL-Template", ""); err != nil || r.VMID != 280 {
		t.Fatalf("pool: %+v %v", r, err)
	}
	if _, err := c.FindByName(ctx, "ubuntu26-base", "", ""); err == nil || !strings.Contains(err.Error(), "280") {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := c.FindByName(ctx, "nope", "", ""); !errors.Is(err, pve.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestRenameAndDelete(t *testing.T) {
	s := pvetest.New(t)
	s.Data("POST /nodes/ul-pve11/qemu/280/config", "UPID:ul-pve11:1:2:3:qmconfig:280:test@pve:")
	s.Data("DELETE /nodes/ul-pve11/qemu/280", "UPID:ul-pve11:1:2:3:qmdestroy:280:test@pve:")
	c := client(t, s)
	ctx := context.Background()
	if upid, err := c.Rename(ctx, "ul-pve11", 280, "ubuntu26-old"); err != nil || !strings.Contains(upid, "qmconfig") {
		t.Fatalf("rename: %q %v", upid, err)
	}
	if upid, err := c.Delete(ctx, "ul-pve11", 280); err != nil || !strings.Contains(upid, "qmdestroy") {
		t.Fatalf("delete: %q %v", upid, err)
	}
	w := s.Writes()
	if len(w) != 2 || w[0].Body["name"] != "ubuntu26-old" || w[1].Method != "DELETE" ||
		!strings.Contains(w[1].Query, "purge=1") || !strings.Contains(w[1].Query, "destroy-unreferenced-disks=1") {
		t.Fatalf("writes: %+v", w)
	}
	if _, err := c.Rename(ctx, "ul-pve11", 280, ""); err == nil {
		t.Fatal("rename without name")
	}
}

func TestNetworksAndClusterStorages(t *testing.T) {
	s := pvetest.New(t)
	s.Data("GET /nodes/ul-pve11/network", []map[string]any{
		{"iface": "vmbr0", "type": "bridge", "active": 1, "bridge_ports": "bond0", "cidr": "10.31.101.11/24"},
		{"iface": "bond0", "type": "bond", "active": 1},
	})
	s.Data("GET /cluster/resources", []map[string]any{
		{"storage": "FS9100", "node": "ul-pve11", "plugintype": "lvm", "status": "available", "shared": 1},
		{"storage": "FS9100", "node": "ul-pve01", "plugintype": "lvm", "status": "available", "shared": 1},
	})
	c := client(t, s)
	ns, err := c.Networks(context.Background(), "ul-pve11")
	if err != nil || len(ns) != 2 || ns[0].Iface != "bond0" || ns[1].BridgePorts != "bond0" || !bool(ns[1].Active) {
		t.Fatalf("networks: %+v %v", ns, err)
	}
	ss, err := c.ClusterStorages(context.Background())
	if err != nil || len(ss) != 2 || ss[0].Node != "ul-pve01" || !bool(ss[0].Shared) {
		t.Fatalf("storages: %+v %v", ss, err)
	}
}

func TestPinnedKnownHosts(t *testing.T) {
	s := tlsFake(t)
	k, err := pve.PinnedKnownHosts(s.APIURL(), pve.Fingerprint(s.Certificate()))
	if err != nil {
		t.Fatal(err)
	}
	if err := resources(t, opts(s, k)); err != nil {
		t.Fatalf("pinned: %v", err)
	}
	k, _ = pve.PinnedKnownHosts(s.APIURL(), strings.Repeat("ab", 32))
	var ce *pve.CertChangedError
	if err := resources(t, opts(s, k)); !errors.As(err, &ce) {
		t.Fatalf("wrong pin: %v", err)
	}
	if _, err := pve.PinnedKnownHosts(s.APIURL(), ""); err == nil {
		t.Fatal("empty fingerprint")
	}
}
