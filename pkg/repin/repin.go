// Package repin lists the stuttgart-things changes that follow a template
// promotion. pvectl only prints them; the pull requests are made by hand.
//
//  1. crossplane/platform/capabilities/proxmoxvm/values.yaml: templateVmId of
//     the OS (for ubuntu26 also the environment default), then a chart bump,
//     a flux release and a flux-infra tag.
//  2. backstage/templates/create-terraform-vm/{template.yaml,
//     machinery-cluster-labul.yaml}: the default of vm_clone_id (the
//     FS9100 copy), in both files together.
package repin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Files in the stuttgart-things repository.
const (
	ValuesFile    = "crossplane/platform/capabilities/proxmoxvm/values.yaml"
	TerraformTpl  = "backstage/templates/create-terraform-vm/template.yaml"
	TerraformLabU = "backstage/templates/create-terraform-vm/machinery-cluster-labul.yaml"
	Repo          = "stuttgart-things/stuttgart-things"
)

// Change is one value to set.
type Change struct {
	Repo    string `json:"repo"`
	File    string `json:"file"`
	Path    string `json:"path"` // YAML path; [*] = every match
	Value   string `json:"value"`
	Current string `json:"current,omitempty"` // filled by ReadCurrent
	Note    string `json:"note,omitempty"`
}

// Unchanged reports whether the file already holds the value.
func (c Change) Unchanged() bool { return c.Current == c.Value }

var osRE = regexp.MustCompile(`^([a-z]+[0-9]+)-`)

// DetectOS derives the OS key from a template name:
// "ubuntu26-base-20261116-0842" -> "ubuntu26", "rocky10-base-…" -> "rocky10".
func DetectOS(name string) string {
	if m := osRE.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// FollowUps are the steps after the values.yaml PR.
var FollowUps = []string{
	"proxmoxvm chart: bump the chart version with the values.yaml change",
	"flux: release the chart (flux-release)",
	"flux-infra: tag the release",
}

// ForPromote returns the changes for a promoted template (VMID templateID)
// and, if copyID > 0, its FS9100 copy.
func ForPromote(osKey string, templateID, copyID int) []Change {
	var out []Change
	tid := fmt.Sprint(templateID)
	if osKey == "" {
		out = append(out, Change{Repo: Repo, File: ValuesFile, Path: "environments.labul.templates.<os>.templateVmId", Value: tid,
			Note: "OS not detected from the template name; pass --os"})
	} else {
		if osKey == "ubuntu26" {
			out = append(out, Change{Repo: Repo, File: ValuesFile, Path: "environments.labul.templateVmId", Value: tid,
				Note: "the default OS; keep equal to templates.ubuntu26"})
		}
		out = append(out, Change{Repo: Repo, File: ValuesFile, Path: "environments.labul.templates." + osKey + ".templateVmId", Value: tid})
	}
	if copyID > 0 {
		cid := fmt.Sprint(copyID)
		note := "both files together"
		if osKey != "" && osKey != "ubuntu26" {
			note += "; create-terraform-vm clones ubuntu26 today, check before repinning to " + osKey
		}
		out = append(out,
			Change{Repo: Repo, File: TerraformTpl, Path: "[*].vm_clone_id.default", Value: cid, Note: note},
			Change{Repo: Repo, File: TerraformLabU, Path: "[*].vm_clone_id.default", Value: cid, Note: note},
		)
	}
	return out
}

// ReadCurrent fills Current from a checkout of the repository at root.
// Changes whose file or path is missing keep Current empty.
func ReadCurrent(root string, changes []Change) error {
	docs := map[string]*yaml.Node{}
	for i := range changes {
		c := &changes[i]
		doc, ok := docs[c.File]
		if !ok {
			b, err := os.ReadFile(filepath.Join(root, c.File))
			if err != nil {
				return err
			}
			var n yaml.Node
			if err := yaml.Unmarshal(b, &n); err != nil {
				return fmt.Errorf("%s: %w", c.File, err)
			}
			doc = &n
			docs[c.File] = doc
		}
		c.Current = strings.Join(lookup(doc, c.Path), ",")
	}
	return nil
}

// lookup resolves a dotted path. A leading "[*]." searches the next key
// anywhere in the document.
func lookup(doc *yaml.Node, path string) []string {
	if strings.HasPrefix(path, "[*].") {
		parts := strings.Split(strings.TrimPrefix(path, "[*]."), ".")
		var out []string
		for _, n := range findKey(doc, parts[0]) {
			if v := walk(n, parts[1:]); v != nil && v.Kind == yaml.ScalarNode {
				out = appendUnique(out, v.Value)
			}
		}
		return out
	}
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if v := walk(root, strings.Split(path, ".")); v != nil && v.Kind == yaml.ScalarNode {
		return []string{v.Value}
	}
	return nil
}

func walk(n *yaml.Node, keys []string) *yaml.Node {
	for _, k := range keys {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}

// findKey returns the values of every mapping key named key.
func findKey(n *yaml.Node, key string) []*yaml.Node {
	var out []*yaml.Node
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				out = append(out, n.Content[i+1])
			}
		}
	}
	for _, c := range n.Content {
		out = append(out, findKey(c, key)...)
	}
	return out
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// Pin is a place in stuttgart-things that references a template VMID.
type Pin struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Key  string `json:"key"`
}

func (p Pin) String() string { return fmt.Sprintf("%s:%d (%s)", p.File, p.Line, p.Key) }

var pinLine = regexp.MustCompile(`^\s*-?\s*(templateVmId|vm_clone_id)\s*:\s*"?(\d+)"?\s*(#.*)?$`)

// pinSkipDirs are not searched (worktrees hold copies of the repo).
var pinSkipDirs = map[string]bool{".git": true, ".claude": true, "node_modules": true}

// FindPins lists the YAML lines under root that set templateVmId or
// vm_clone_id to vmid: claims, XRs and backstage runs that clone from it.
// The repin targets (ValuesFile, TerraformTpl, TerraformLabU) are left
// out: a promotion repins those on purpose.
func FindPins(root string, vmid int) ([]Pin, error) {
	want := strconv.Itoa(vmid)
	skip := map[string]bool{ValuesFile: true, TerraformTpl: true, TerraformLabU: true}
	var pins []Pin
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if pinSkipDirs[d.Name()] && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if skip[filepath.ToSlash(rel)] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := pinLine.FindStringSubmatch(line); m != nil && m[2] == want {
				pins = append(pins, Pin{File: filepath.ToSlash(rel), Line: i + 1, Key: m[1]})
			}
		}
		return nil
	})
	return pins, err
}
