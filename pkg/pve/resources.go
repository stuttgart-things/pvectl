package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Flag is a Proxmox boolean: 0/1, "0"/"1" or true/false on the wire.
type Flag bool

// UnmarshalJSON accepts every encoding PVE uses for booleans.
func (f *Flag) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	switch s {
	case "1", "true":
		*f = true
	case "0", "false", "", "null":
		*f = false
	default:
		return fmt.Errorf("not a boolean: %s", b)
	}
	return nil
}

// Resource is a guest from /cluster/resources?type=vm. It carries no
// description: the endpoint does not return one.
type Resource struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Type     string `json:"type"` // qemu or lxc
	Status   string `json:"status"`
	Template Flag   `json:"template"`
	Tags     string `json:"tags,omitempty"`
	Pool     string `json:"pool,omitempty"`
	Uptime   int64  `json:"uptime"`
	HAState  string `json:"hastate,omitempty"`
	MaxMem   int64  `json:"maxmem,omitempty"`
	MaxDisk  int64  `json:"maxdisk,omitempty"`
	MaxCPU   int    `json:"maxcpu,omitempty"`
}

// Kind selects guests by their template flag.
type Kind int

const (
	KindVMs       Kind = iota // only guests that are not templates
	KindTemplates             // only templates
	KindAll                   // both
)

// Resources lists every guest of the cluster, sorted by name then VMID.
func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	var rs []Resource
	if err := c.get(ctx, "/cluster/resources?type=vm", &rs); err != nil {
		return nil, err
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Name != rs[j].Name {
			return rs[i].Name < rs[j].Name
		}
		return rs[i].VMID < rs[j].VMID
	})
	return rs, nil
}

// Filter keeps the guests of the given kind whose name contains filter, or
// whose VMID equals it. An empty filter keeps all of that kind.
func Filter(rs []Resource, filter string, kind Kind) []Resource {
	id, idErr := strconv.Atoi(filter)
	var out []Resource
	for _, r := range rs {
		if idErr == nil && r.VMID == id {
			// an exact VMID finds the guest whatever kind was asked for
			out = append(out, r)
			continue
		}
		switch kind {
		case KindVMs:
			if bool(r.Template) {
				continue
			}
		case KindTemplates:
			if !bool(r.Template) {
				continue
			}
		}
		if filter == "" || strings.Contains(r.Name, filter) {
			out = append(out, r)
		}
	}
	return out
}

// FindVM returns the guest with the given VMID (VM or template).
func (c *Client) FindVM(ctx context.Context, vmid int) (Resource, error) {
	rs, err := c.Resources(ctx)
	if err != nil {
		return Resource{}, err
	}
	return FindIn(rs, vmid)
}

// FindIn looks a VMID up in a resource list.
func FindIn(rs []Resource, vmid int) (Resource, error) {
	for _, r := range rs {
		if r.VMID == vmid {
			return r, nil
		}
	}
	return Resource{}, fmt.Errorf("VMID %d: %w", vmid, ErrNotFound)
}

// FindByName returns the guests named exactly name.
func FindByName(rs []Resource, name string) []Resource {
	var out []Resource
	for _, r := range rs {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

// Node is a cluster member.
type Node struct {
	Node   string  `json:"node"`
	Status string  `json:"status"`
	CPU    float64 `json:"cpu,omitempty"`
	MaxCPU int     `json:"maxcpu,omitempty"`
	Mem    int64   `json:"mem,omitempty"`
	MaxMem int64   `json:"maxmem,omitempty"`
	Uptime int64   `json:"uptime,omitempty"`
}

// Nodes lists the cluster nodes, sorted by name.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var ns []Node
	if err := c.get(ctx, "/nodes", &ns); err != nil {
		return nil, err
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].Node < ns[j].Node })
	return ns, nil
}

// Storage is a storage as one node sees it.
type Storage struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Shared  Flag   `json:"shared"`
	Active  Flag   `json:"active"`
	Enabled Flag   `json:"enabled"`
	Avail   int64  `json:"avail"`
	Total   int64  `json:"total"`
}

// Storages lists the storages of a node that can hold VM disks (content
// images), sorted by name.
func (c *Client) Storages(ctx context.Context, node string) ([]Storage, error) {
	var ss []Storage
	if err := c.get(ctx, "/nodes/"+node+"/storage?content=images", &ss); err != nil {
		return nil, err
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].Storage < ss[j].Storage })
	return ss, nil
}

// NextID asks the cluster for a free VMID.
func (c *Client) NextID(ctx context.Context) (int, error) {
	var raw json.RawMessage
	if err := c.get(ctx, "/cluster/nextid", &raw); err != nil {
		return 0, err
	}
	return atoiJSON(raw)
}

// IDFree reports whether vmid is unused (GET /cluster/nextid?vmid=N answers
// with an error if it is taken).
func (c *Client) IDFree(ctx context.Context, vmid int) bool {
	var raw json.RawMessage
	return c.get(ctx, fmt.Sprintf("/cluster/nextid?vmid=%d", vmid), &raw) == nil
}

func atoiJSON(raw json.RawMessage) (int, error) {
	s := strings.Trim(string(raw), `"`)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("unexpected VMID %s", raw)
	}
	return n, nil
}
