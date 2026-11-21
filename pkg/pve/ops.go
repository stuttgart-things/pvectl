package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// Lookups and operations for other callers of the library (the Dagger
// packer module): find a guest by name, rename, delete, node networks and
// cluster storages.

// FindByName returns the guest (VM or template) with exactly this name,
// optionally only in pool and/or on node. No match is ErrNotFound; more
// than one match is an error that lists the VMIDs.
func (c *Client) FindByName(ctx context.Context, name, pool, node string) (Resource, error) {
	if name == "" {
		return Resource{}, fmt.Errorf("no name given")
	}
	rs, err := c.Resources(ctx)
	if err != nil {
		return Resource{}, err
	}
	var hits []Resource
	for _, r := range rs {
		if r.Name != name || (pool != "" && r.Pool != pool) || (node != "" && r.Node != node) {
			continue
		}
		hits = append(hits, r)
	}
	switch len(hits) {
	case 0:
		return Resource{}, fmt.Errorf("no VM or template named %q (pool %q, node %q): %w", name, pool, node, ErrNotFound)
	case 1:
		return hits[0], nil
	}
	ids := make([]int, len(hits))
	for i, r := range hits {
		ids[i] = r.VMID
	}
	return Resource{}, fmt.Errorf("%d guests named %q: VMIDs %v", len(hits), name, ids)
}

// RenameCall is the API request Rename sends.
func RenameCall(node string, vmid int, name string) APICall {
	return callOf("POST", fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid), map[string]any{"name": name})
}

// Rename sets the name of a VM or template. It uses the async config
// endpoint (POST) and returns the task UPID, "" if PVE sends none.
func (c *Client) Rename(ctx context.Context, node string, vmid int, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("no new name given")
	}
	var raw json.RawMessage
	if err := c.post(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid), map[string]any{"name": name}, &raw); err != nil {
		return "", err
	}
	var upid string
	_ = json.Unmarshal(raw, &upid)
	return upid, nil
}

// DeleteCall is the API request Delete sends.
func DeleteCall(node string, vmid int) APICall {
	return APICall{Method: "DELETE", Path: fmt.Sprintf("/nodes/%s/qemu/%d", node, vmid),
		Params: map[string]string{"purge": "1", "destroy-unreferenced-disks": "1"}}
}

// Delete destroys a VM or template with its disks, and removes it from
// backup jobs, replication and HA (purge). It returns the task UPID.
func (c *Client) Delete(ctx context.Context, node string, vmid int) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d?purge=1&destroy-unreferenced-disks=1", node, vmid)
	var upid string
	err := c.wrap(http.MethodDelete, path, c.api.Delete(ctx, path, &upid))
	return upid, err
}

// Network is a network interface of a node (bridge, bond, NIC, VLAN).
type Network struct {
	Iface       string `json:"iface"`
	Type        string `json:"type"`
	Active      Flag   `json:"active"`
	Autostart   Flag   `json:"autostart"`
	Method      string `json:"method,omitempty"`
	CIDR        string `json:"cidr,omitempty"`
	Address     string `json:"address,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	BridgePorts string `json:"bridge_ports,omitempty"`
	VLANAware   Flag   `json:"bridge_vlan_aware,omitempty"`
}

// Networks lists the network interfaces of a node, sorted by name.
func (c *Client) Networks(ctx context.Context, node string) ([]Network, error) {
	var ns []Network
	if err := c.get(ctx, "/nodes/"+node+"/network", &ns); err != nil {
		return nil, err
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].Iface < ns[j].Iface })
	return ns, nil
}

// ClusterStorage is a storage on one node, from /cluster/resources.
type ClusterStorage struct {
	Storage    string `json:"storage"`
	Node       string `json:"node"`
	PluginType string `json:"plugintype"`
	Status     string `json:"status"`
	Shared     Flag   `json:"shared"`
	Content    string `json:"content"`
	Disk       int64  `json:"disk"`
	MaxDisk    int64  `json:"maxdisk"`
}

// ClusterStorages lists every storage on every node, sorted by storage
// then node.
func (c *Client) ClusterStorages(ctx context.Context) ([]ClusterStorage, error) {
	var ss []ClusterStorage
	if err := c.get(ctx, "/cluster/resources?type=storage", &ss); err != nil {
		return nil, err
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].Storage != ss[j].Storage {
			return ss[i].Storage < ss[j].Storage
		}
		return ss[i].Node < ss[j].Node
	})
	return ss, nil
}

// PinnedKnownHosts is an in-memory known-hosts set with one pin: the
// certificate of apiURL must have this SHA-256 (AB:CD:… or hex). For
// callers without a known-hosts file, e.g. a pipeline that gets the
// fingerprint as a parameter.
func PinnedKnownHosts(apiURL, fingerprint string) (*KnownHosts, error) {
	hp, err := hostPort(apiURL)
	if err != nil {
		return nil, err
	}
	if fingerprint == "" {
		return nil, fmt.Errorf("no fingerprint given")
	}
	k, _ := LoadKnownHosts("")
	k.Set(hp, fingerprint)
	return k, nil
}
