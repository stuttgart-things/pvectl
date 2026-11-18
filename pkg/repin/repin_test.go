package repin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectOS(t *testing.T) {
	for name, want := range map[string]string{
		"ubuntu26-base-20261116-0842": "ubuntu26",
		"rocky10-base-20261115-2011":  "rocky10",
		"sthings-u26":                 "",
	} {
		if got := DetectOS(name); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func write(t *testing.T, root, file, content string) {
	t.Helper()
	p := filepath.Join(root, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadCurrent(t *testing.T) {
	root := t.TempDir()
	write(t, root, ValuesFile, `
environments:
  labul:
    templateVmId: "280"
    templates:
      ubuntu26:
        templateVmId: "280"  # = templateVmId above
      rocky10:
        templateVmId: "279"
`)
	tf := `
spec:
  parameters:
    - properties:
        vm_clone_id:
          # comment
          type: string
          default: "282"
`
	write(t, root, TerraformTpl, tf)
	write(t, root, TerraformLabU, tf)

	cs := ForPromote("ubuntu26", 290, 291)
	if err := ReadCurrent(root, cs); err != nil {
		t.Fatal(err)
	}
	want := []string{"280", "280", "282", "282"}
	for i, c := range cs {
		if c.Current != want[i] {
			t.Errorf("%s %s: current %q, want %q", c.File, c.Path, c.Current, want[i])
		}
	}
	cs = ForPromote("rocky10", 279, 0)
	if err := ReadCurrent(root, cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || !cs[0].Unchanged() {
		t.Fatalf("%+v", cs)
	}
}
