package pve

import (
	"context"
	"fmt"
)

// HAStatus is an entry of /cluster/ha/status/current (quorum, master, lrm
// or service).
type HAStatus struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	SID          string `json:"sid,omitempty"`
	Node         string `json:"node,omitempty"`
	State        string `json:"state,omitempty"`
	RequestState string `json:"request_state,omitempty"`
	Status       string `json:"status,omitempty"`
}

// HAResource is a configured HA resource. The free-text comment is left
// out on purpose, like a VM description.
type HAResource struct {
	SID         string `json:"sid"`
	Type        string `json:"type,omitempty"`
	State       string `json:"state,omitempty"`
	Group       string `json:"group,omitempty"`
	MaxRestart  int    `json:"max_restart,omitempty"`
	MaxRelocate int    `json:"max_relocate,omitempty"`
}

// HASID is the HA service ID of a VM.
func HASID(vmid int) string { return fmt.Sprintf("vm:%d", vmid) }

// HAStatusAll reads the current HA status of the cluster.
func (c *Client) HAStatusAll(ctx context.Context) ([]HAStatus, error) {
	var out []HAStatus
	err := c.get(ctx, "/cluster/ha/status/current", &out)
	return out, err
}

// HAState returns the HA status entry of a service; ok is false if the
// service is not under HA.
func (c *Client) HAState(ctx context.Context, sid string) (HAStatus, bool, error) {
	all, err := c.HAStatusAll(ctx)
	if err != nil {
		return HAStatus{}, false, err
	}
	for _, s := range all {
		if s.SID == sid {
			return s, true, nil
		}
	}
	return HAStatus{}, false, nil
}

// HAResource reads the HA resource config of a service (ErrNotFound if the
// service is not under HA).
func (c *Client) HAResource(ctx context.Context, sid string) (HAResource, error) {
	var r HAResource
	err := c.get(ctx, "/cluster/ha/resources/"+sid, &r)
	if r.SID == "" {
		r.SID = sid
	}
	return r, err
}

// HASetStateCall is the API request HASetState sends.
func HASetStateCall(sid, state string) APICall {
	return callOf("PUT", "/cluster/ha/resources/"+sid, map[string]any{"state": state})
}

// HASetState sets the requested state of an HA resource.
func (c *Client) HASetState(ctx context.Context, sid, state string) error {
	return c.put(ctx, "/cluster/ha/resources/"+sid, map[string]any{"state": state}, nil)
}
