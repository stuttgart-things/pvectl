package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Config is a VM configuration as key -> value strings, WITHOUT the
// description. Build it only through Sanitize (Client.Config does).
type Config map[string]string

// Keys that never leave this package. `description` holds the notes (in the
// LabUL-Template pool: plaintext passwords); there is no option to keep it.
var droppedKeys = map[string]bool{"description": true}

// Keys whose value is replaced by "(set)".
var maskedKeys = map[string]bool{"cipassword": true}

// Masked is the value shown for a masked key.
const Masked = "(set)"

// Sanitize turns a raw API config into a Config: it drops the description,
// masks the cloud-init password and renders every value as a string.
func Sanitize(raw map[string]any) Config {
	cfg := Config{}
	for k, v := range raw {
		if droppedKeys[strings.ToLower(k)] {
			continue
		}
		if maskedKeys[strings.ToLower(k)] {
			cfg[k] = Masked
			continue
		}
		switch t := v.(type) {
		case string:
			cfg[k] = t
		case json.Number:
			cfg[k] = t.String()
		case float64:
			cfg[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			if t {
				cfg[k] = "1"
			} else {
				cfg[k] = "0"
			}
		case nil:
			cfg[k] = ""
		default:
			b, _ := json.Marshal(t)
			cfg[k] = string(b)
		}
	}
	return cfg
}

// Config reads the current config of a VM or template, without description.
func (c *Client) Config(ctx context.Context, node string, vmid int) (Config, error) {
	var raw map[string]any
	if err := c.get(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid), &raw); err != nil {
		return nil, err
	}
	return Sanitize(raw), nil
}

// Keys returns the config keys in a stable order.
func (cfg Config) Keys() []string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IsTemplate reports the template flag.
func (cfg Config) IsTemplate() bool { return cfg["template"] == "1" }

var diskKey = regexp.MustCompile(`^(ide|sata|scsi|virtio|efidisk|tpmstate|unused)\d+$`)

// Disk is one disk (or the cloud-init drive) of a config.
type Disk struct {
	Key       string `json:"key"`
	Volume    string `json:"volume"`
	Storage   string `json:"storage"`
	CloudInit bool   `json:"cloudinit,omitempty"`
	Media     string `json:"media,omitempty"`
}

// Disks lists the disks of a config, CD-ROMs without a volume excluded.
func (cfg Config) Disks() []Disk {
	var out []Disk
	for _, k := range cfg.Keys() {
		if !diskKey.MatchString(k) {
			continue
		}
		v := cfg[k]
		vol := strings.SplitN(v, ",", 2)[0]
		if vol == "none" || vol == "" {
			continue
		}
		d := Disk{Key: k, Volume: vol}
		if i := strings.Index(vol, ":"); i > 0 {
			d.Storage = vol[:i]
		}
		d.CloudInit = strings.Contains(vol, "cloudinit")
		if m := regexp.MustCompile(`media=(\w+)`).FindStringSubmatch(v); m != nil {
			d.Media = m[1]
		}
		if d.Media == "cdrom" && !d.CloudInit {
			continue
		}
		out = append(out, d)
	}
	return out
}

// Check is the result of one sanity check of a config.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"` // not OK, but does not fail the checks
	Detail string `json:"detail"`
}

// CheckConfig runs the template sanity checks:
//
//   - cpu: host. EL10 needs x86-64-v3; the default kvm64 lacks it, and the
//     Packer plugin does not always carry cpu_type through (#4650).
//   - agent enabled, or Proxmox never learns the guest IP.
//   - a cloud-init drive on a VM, or it keeps the template hostname/user.
//     On a template a missing drive is expected and passes: the LabUL
//     templates (211, 266, 280, 282 in 2026-11) have none, bpg
//     (initialization) and Telmate add one to every clone.
func CheckConfig(cfg Config) []Check {
	var checks []Check

	cpu := cfg["cpu"]
	cpuType := strings.SplitN(cpu, ",", 2)[0]
	cpuType = strings.TrimPrefix(cpuType, "cputype=")
	switch {
	case cpu == "":
		checks = append(checks, Check{Name: "cpu", Detail: "unset (Proxmox default kvm64/x86-64-v2-AES), want host"})
	case cpuType == "host":
		checks = append(checks, Check{Name: "cpu", OK: true, Detail: cpu})
	default:
		checks = append(checks, Check{Name: "cpu", Detail: cpu + ", want host"})
	}

	agent := cfg["agent"]
	if agentEnabled(agent) {
		checks = append(checks, Check{Name: "agent", OK: true, Detail: agent})
	} else {
		d := agent
		if d == "" {
			d = "unset"
		}
		checks = append(checks, Check{Name: "agent", Detail: d + ", want enabled=1"})
	}

	ci := ""
	for _, d := range cfg.Disks() {
		if d.CloudInit {
			ci = d.Key + ": " + d.Volume
			break
		}
	}
	switch {
	case ci != "":
		checks = append(checks, Check{Name: "cloud-init drive", OK: true, Detail: ci})
	case cfg.IsTemplate():
		checks = append(checks, Check{Name: "cloud-init drive", OK: true,
			Detail: "none on the template (expected: bpg initialization / Telmate add one per clone)"})
	default:
		checks = append(checks, Check{Name: "cloud-init drive", Detail: "none (the VM keeps the template hostname)"})
	}
	return checks
}

// ChecksOK reports whether every check passed (warnings pass).
func ChecksOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK && !c.Warn {
			return false
		}
	}
	return true
}

func agentEnabled(v string) bool {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "1" || part == "enabled=1" {
			return true
		}
	}
	return false
}
