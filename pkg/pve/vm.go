package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// VMStatus is /nodes/{node}/qemu/{vmid}/status/current, reduced to the
// fields pvectl uses.
type VMStatus struct {
	VMID      int            `json:"vmid"`
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	QMPStatus string         `json:"qmpstatus,omitempty"`
	Uptime    int64          `json:"uptime"`
	Template  Flag           `json:"template"`
	Lock      string         `json:"lock,omitempty"`
	HA        map[string]any `json:"ha,omitempty"`
}

// Status reads the current status of a VM.
func (c *Client) Status(ctx context.Context, node string, vmid int) (VMStatus, error) {
	var st VMStatus
	err := c.get(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/status/current", node, vmid), &st)
	return st, err
}

// Start starts a VM and returns the task UPID.
func (c *Client) Start(ctx context.Context, node string, vmid int) (string, error) {
	var upid string
	err := c.post(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/status/start", node, vmid), nil, &upid)
	return upid, err
}

// GuestIPv4 asks the guest agent for the IPv4 addresses of the ens*/eth*
// interfaces.
func (c *Client) GuestIPv4(ctx context.Context, node string, vmid int) ([]string, error) {
	var r struct {
		Result []struct {
			Name        string `json:"name"`
			IPAddresses []struct {
				Type    string `json:"ip-address-type"`
				Address string `json:"ip-address"`
			} `json:"ip-addresses"`
		} `json:"result"`
	}
	if err := c.get(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid), &r); err != nil {
		return nil, err
	}
	var ips []string
	for _, i := range r.Result {
		if !strings.HasPrefix(i.Name, "ens") && !strings.HasPrefix(i.Name, "eth") {
			continue
		}
		for _, a := range i.IPAddresses {
			if a.Type == "ipv4" {
				ips = append(ips, a.Address)
			}
		}
	}
	return ips, nil
}

// MigrateCheck is GET /nodes/{node}/qemu/{vmid}/migrate?target=…: what a
// migration to target would need.
type MigrateCheck struct {
	Running         bool                      `json:"running"`
	LocalDisks      []string                  `json:"local_disks"`
	LocalResources  []string                  `json:"local_resources,omitempty"`
	AllowedNodes    []string                  `json:"allowed_nodes,omitempty"`
	NotAllowedNodes map[string]map[string]any `json:"not_allowed_nodes,omitempty"`
}

// Blocked returns why target is not allowed, or nil.
func (m MigrateCheck) Blocked(target string) map[string]any {
	if b, ok := m.NotAllowedNodes[target]; ok && len(b) > 0 {
		return b
	}
	return nil
}

// UnavailableStorages lists the storages target lacks, if that is the reason.
func (m MigrateCheck) UnavailableStorages(target string) []string {
	b := m.Blocked(target)
	if b == nil {
		return nil
	}
	raw, _ := json.Marshal(b["unavailable_storages"])
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}

// MigrateCheck asks Proxmox whether and how vmid can move to target.
func (c *Client) MigrateCheck(ctx context.Context, node string, vmid int, target string) (MigrateCheck, error) {
	var raw struct {
		Running         Flag                      `json:"running"`
		LocalDisks      []map[string]any          `json:"local_disks"`
		LocalResources  []string                  `json:"local_resources"`
		AllowedNodes    []string                  `json:"allowed_nodes"`
		NotAllowedNodes map[string]map[string]any `json:"not_allowed_nodes"`
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/migrate?target=%s", node, vmid, url.QueryEscape(target))
	if err := c.get(ctx, path, &raw); err != nil {
		return MigrateCheck{}, err
	}
	m := MigrateCheck{
		Running:         bool(raw.Running),
		LocalResources:  raw.LocalResources,
		AllowedNodes:    raw.AllowedNodes,
		NotAllowedNodes: raw.NotAllowedNodes,
	}
	for _, d := range raw.LocalDisks {
		if v, ok := d["volid"].(string); ok {
			m.LocalDisks = append(m.LocalDisks, v)
		}
	}
	sort.Strings(m.LocalDisks)
	return m, nil
}

// APICall describes a write request, for --dry-run plans. Params never
// carry a description (see Redact).
type APICall struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Params map[string]string `json:"params,omitempty"`
}

func (a APICall) String() string {
	keys := make([]string, 0, len(a.Params))
	for k := range a.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+a.Params[k])
	}
	return strings.TrimSpace(a.Method + " " + a.Path + " " + strings.Join(parts, " "))
}

// Redacted is what a plan shows in place of a description parameter.
const Redacted = "(not shown)"

func callOf(method, path string, data map[string]any) APICall {
	p := map[string]string{}
	for k, v := range data {
		if droppedKeys[k] {
			p[k] = Redacted
			continue
		}
		p[k] = fmt.Sprint(v)
	}
	return APICall{Method: method, Path: path, Params: p}
}

// MigrateRequest is a VM migration.
type MigrateRequest struct {
	Node           string `json:"node"`
	VMID           int    `json:"vmid"`
	Target         string `json:"target"`
	TargetStorage  string `json:"targetstorage,omitempty"`
	Online         bool   `json:"online"`
	WithLocalDisks bool   `json:"with_local_disks"`
}

func (r MigrateRequest) data() map[string]any {
	d := map[string]any{"target": r.Target}
	if r.Online {
		d["online"] = 1
	}
	if r.WithLocalDisks {
		d["with-local-disks"] = 1
	}
	if r.TargetStorage != "" {
		d["targetstorage"] = r.TargetStorage
	}
	return d
}

// Call is the API request Migrate sends.
func (r MigrateRequest) Call() APICall {
	return callOf("POST", fmt.Sprintf("/nodes/%s/qemu/%d/migrate", r.Node, r.VMID), r.data())
}

// Migrate starts a migration and returns the task UPID.
func (c *Client) Migrate(ctx context.Context, r MigrateRequest) (string, error) {
	var upid string
	err := c.post(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/migrate", r.Node, r.VMID), r.data(), &upid)
	return upid, err
}

// CloneRequest is a VM/template clone.
type CloneRequest struct {
	Node        string `json:"node"` // node of the source
	VMID        int    `json:"vmid"` // source
	NewID       int    `json:"newid"`
	Name        string `json:"name"`
	Full        bool   `json:"full"`
	Storage     string `json:"storage,omitempty"`
	Target      string `json:"target,omitempty"` // only for a source on shared storage
	Pool        string `json:"pool,omitempty"`
	Description string `json:"-"` // written to the clone, never shown
}

func (r CloneRequest) data() map[string]any {
	d := map[string]any{"newid": r.NewID, "name": r.Name}
	if r.Full {
		d["full"] = 1
	}
	if r.Storage != "" {
		d["storage"] = r.Storage
	}
	if r.Target != "" {
		d["target"] = r.Target
	}
	if r.Pool != "" {
		d["pool"] = r.Pool
	}
	if r.Description != "" {
		d["description"] = r.Description
	}
	return d
}

// Call is the API request Clone sends (description redacted).
func (r CloneRequest) Call() APICall {
	return callOf("POST", fmt.Sprintf("/nodes/%s/qemu/%d/clone", r.Node, r.VMID), r.data())
}

// Clone starts a clone and returns the task UPID.
func (c *Client) Clone(ctx context.Context, r CloneRequest) (string, error) {
	var upid string
	err := c.post(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/clone", r.Node, r.VMID), r.data(), &upid)
	return upid, err
}

// TemplateCall is the API request ConvertToTemplate sends.
func TemplateCall(node string, vmid int) APICall {
	return callOf("POST", fmt.Sprintf("/nodes/%s/qemu/%d/template", node, vmid), nil)
}

// ConvertToTemplate turns a stopped VM into a template. Newer PVE versions
// answer with a task UPID, older ones with null; the UPID is "" then.
func (c *Client) ConvertToTemplate(ctx context.Context, node string, vmid int) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/template", node, vmid), nil, &raw); err != nil {
		return "", err
	}
	var upid string
	_ = json.Unmarshal(raw, &upid)
	return upid, nil
}
