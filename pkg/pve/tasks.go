package pve

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Task is an entry of a task list.
type Task struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"` // usually the VMID
	User      string `json:"user"`
	Status    string `json:"status,omitempty"` // exit status once finished ("OK", an error); empty while running
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime,omitempty"`
}

// Running reports whether the task has not finished yet.
func (t Task) Running() bool { return t.EndTime == 0 && t.Status == "" }

// TaskFilter narrows a task list.
type TaskFilter struct {
	Node   string // only this node ("" = cluster-wide /cluster/tasks)
	VMID   int    // only tasks of this guest (0 = all)
	Type   string // type prefix, e.g. "qm" or "qmigrate"
	Errors bool   // only failed tasks
	Since  time.Time
	Limit  int // per node; 0 = 50
}

// Tasks lists tasks, oldest first. Without a node it reads /cluster/tasks
// (the recent tasks of every node).
func (c *Client) Tasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	var ts []Task
	if f.Node == "" {
		if err := c.get(ctx, "/cluster/tasks", &ts); err != nil {
			return nil, err
		}
	} else {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(limit))
		q.Set("source", "all")
		if f.VMID != 0 {
			q.Set("vmid", strconv.Itoa(f.VMID))
		}
		if f.Errors {
			q.Set("errors", "1")
		}
		if !f.Since.IsZero() {
			q.Set("since", strconv.FormatInt(f.Since.Unix(), 10))
		}
		if err := c.get(ctx, "/nodes/"+f.Node+"/tasks?"+q.Encode(), &ts); err != nil {
			return nil, err
		}
	}
	var out []Task
	for _, t := range ts {
		if f.VMID != 0 && t.ID != strconv.Itoa(f.VMID) {
			continue
		}
		if f.Type != "" && !strings.HasPrefix(t.Type, f.Type) {
			continue
		}
		if f.Errors && (t.Status == "" || t.Status == "OK") {
			continue
		}
		if !f.Since.IsZero() && t.StartTime < f.Since.Unix() {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartTime < out[j].StartTime })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// UPID is a parsed task ID: UPID:node:pid:pstart:starttime:type:id:user:
type UPID struct {
	Raw       string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	StartTime int64  `json:"starttime"`
}

// ParseUPID splits a UPID. The start time is hex encoded.
func ParseUPID(s string) (UPID, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 8 || parts[0] != "UPID" {
		return UPID{}, fmt.Errorf("not a UPID: %q", s)
	}
	start, _ := strconv.ParseInt(parts[4], 16, 64)
	return UPID{Raw: s, Node: parts[1], Type: parts[5], ID: parts[6], User: parts[7], StartTime: start}, nil
}

// TaskStatus is the state of one task.
type TaskStatus struct {
	UPID       string `json:"upid"`
	Node       string `json:"node"`
	Type       string `json:"type"`
	ID         string `json:"id"`
	User       string `json:"user"`
	Status     string `json:"status"`               // running | stopped
	ExitStatus string `json:"exitstatus,omitempty"` // OK or the error, once stopped
	StartTime  int64  `json:"starttime"`
}

// Done reports whether the task stopped.
func (s TaskStatus) Done() bool { return s.Status == "stopped" }

// OK reports whether the task stopped with exit status OK.
func (s TaskStatus) OK() bool { return s.Done() && s.ExitStatus == "OK" }

func taskPath(upid UPID) string {
	return "/nodes/" + upid.Node + "/tasks/" + url.PathEscape(upid.Raw)
}

// TaskStatus reads the status of a task.
func (c *Client) TaskStatus(ctx context.Context, upid string) (TaskStatus, error) {
	u, err := ParseUPID(upid)
	if err != nil {
		return TaskStatus{}, err
	}
	var s TaskStatus
	if err := c.get(ctx, taskPath(u)+"/status", &s); err != nil {
		return TaskStatus{}, err
	}
	if s.UPID == "" {
		s.UPID = upid
	}
	return s, nil
}

// LogLine is a line of a task log; N counts from 1.
type LogLine struct {
	N int    `json:"n"`
	T string `json:"t"`
}

// TaskLog reads up to limit lines of a task log from line start (0-based).
func (c *Client) TaskLog(ctx context.Context, upid string, start, limit int) ([]LogLine, error) {
	u, err := ParseUPID(upid)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	var lines []LogLine
	path := fmt.Sprintf("%s/log?start=%d&limit=%d", taskPath(u), start, limit)
	if err := c.get(ctx, path, &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

// TaskEvent is what WaitTask reports while it waits.
type TaskEvent struct {
	Line    string  // a new log line ("" if none)
	Percent float64 // last progress seen in the log, -1 if none yet
}

// WaitOptions tune WaitTask.
type WaitOptions struct {
	Poll    time.Duration   // 0 = 3s
	OnEvent func(TaskEvent) // called for every new log line
}

var percentRE = regexp.MustCompile(`\((\d+(?:\.\d+)?)%\)|(\d+(?:\.\d+)?)%`)

// ParsePercent finds a progress percentage in a task log line, as clone
// ("transferred 3.2 GiB of 32.0 GiB (10.00%)") and migrate print it.
func ParsePercent(line string) (float64, bool) {
	m := percentRE.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	s := m[1]
	if s == "" {
		s = m[2]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 100 {
		return 0, false
	}
	return f, true
}

// WaitTask follows a task until it stops (or ctx ends), streaming its log
// through opts.OnEvent. A stopped task with an exit status other than OK
// returns an error wrapping ErrTaskFailed.
func (c *Client) WaitTask(ctx context.Context, upid string, opts WaitOptions) (TaskStatus, error) {
	poll := opts.Poll
	if poll == 0 {
		poll = 3 * time.Second
	}
	next := 0
	percent := -1.0
	drain := func() {
		for {
			lines, err := c.TaskLog(ctx, upid, next, 500)
			if err != nil || len(lines) == 0 {
				return
			}
			for _, l := range lines {
				if l.N <= next {
					continue
				}
				next = l.N
				if l.T == "no content" && l.N == 1 {
					continue
				}
				if p, ok := ParsePercent(l.T); ok {
					percent = p
				}
				if opts.OnEvent != nil {
					opts.OnEvent(TaskEvent{Line: l.T, Percent: percent})
				}
			}
			if len(lines) < 500 {
				return
			}
		}
	}
	for {
		st, err := c.TaskStatus(ctx, upid)
		if err != nil {
			return st, err
		}
		drain()
		if st.Done() {
			if !st.OK() {
				return st, fmt.Errorf("task %s %s: %s: %w", st.Type, st.ID, st.ExitStatus, ErrTaskFailed)
			}
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, fmt.Errorf("stopped waiting for %s (the task keeps running; follow it with: pvectl tasks --follow '%s'): %w",
				upid, upid, ctx.Err())
		case <-time.After(poll):
		}
	}
}
