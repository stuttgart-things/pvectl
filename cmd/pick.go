package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// pickGuest asks for a VM or template from a filterable list.
func pickGuest(ctx context.Context, c *pve.Client, kind pve.Kind, title string) (pve.Resource, error) {
	rs, err := c.Resources(ctx)
	if err != nil {
		return pve.Resource{}, err
	}
	rs = pve.Filter(rs, "", kind)
	if len(rs) == 0 {
		return pve.Resource{}, fmt.Errorf("no guests to choose from: %w", pve.ErrNotFound)
	}
	options := make([]huh.Option[int], 0, len(rs))
	for _, r := range rs {
		label := fmt.Sprintf("%-5d %-40s %-9s %s", r.VMID, r.Name, r.Node, r.Status)
		if r.Template {
			label = fmt.Sprintf("%-5d %-40s %-9s template", r.VMID, r.Name, r.Node)
		}
		options = append(options, huh.NewOption(label, r.VMID))
	}
	var id int
	sel := huh.NewSelect[int]().Title(title).Description("type / to filter").Options(options...).Value(&id).Height(15)
	if err := huh.NewForm(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return pve.Resource{}, err
	}
	return pve.FindIn(rs, id)
}

// pickNode asks for a node other than exclude.
func pickNode(ctx context.Context, c *pve.Client, title, exclude, preferred string) (string, error) {
	ns, err := c.Nodes(ctx)
	if err != nil {
		return "", err
	}
	var options []huh.Option[string]
	for _, n := range ns {
		if n.Node == exclude {
			continue
		}
		label := n.Node
		if n.Status != "online" {
			label += "  (" + n.Status + ")"
		}
		options = append(options, huh.NewOption(label, n.Node))
	}
	if len(options) == 0 {
		return "", fmt.Errorf("no other node: %w", pve.ErrNotFound)
	}
	node := preferred
	sel := huh.NewSelect[string]().Title(title).Options(options...).Value(&node)
	if err := huh.NewForm(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return "", err
	}
	return node, nil
}

// pickStorage asks for a storage on node that holds VM images.
func pickStorage(ctx context.Context, c *pve.Client, node, title string, sharedOnly bool, preferred string) (string, error) {
	ss, err := c.Storages(ctx, node)
	if err != nil {
		return "", err
	}
	var options []huh.Option[string]
	for _, s := range ss {
		if sharedOnly && !bool(s.Shared) {
			continue
		}
		shared := "local"
		if s.Shared {
			shared = "shared"
		}
		label := fmt.Sprintf("%-16s %-8s %-6s %s free", s.Storage, s.Type, shared, humanBytes(s.Avail))
		options = append(options, huh.NewOption(label, s.Storage))
	}
	if len(options) == 0 {
		return "", fmt.Errorf("no storage for VM images on %s: %w", node, pve.ErrNotFound)
	}
	st := preferred
	sel := huh.NewSelect[string]().Title(title).Options(options...).Value(&st)
	if err := huh.NewForm(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return "", err
	}
	return st, nil
}

// confirm shows a summary and asks yes/no. No means errAborted.
func confirm(ctx context.Context, title string, summary []string) error {
	fmt.Println()
	fmt.Println(boxStyle.Render(titleStyle.Render(title) + "\n\n" + strings.Join(summary, "\n")))
	ok := false
	c := huh.NewConfirm().Title("Proceed?").Affirmative("Yes").Negative("No").Value(&ok)
	if err := huh.NewForm(huh.NewGroup(c)).RunWithContext(ctx); err != nil {
		return err
	}
	if !ok {
		return errAborted
	}
	return nil
}

// writeGate decides for a write command: dry-run -> no write; interactive
// -> confirm unless --yes; programmatic -> --yes required.
func writeGate(ctx context.Context, dryRun, yes bool, title string, summary []string) (proceed bool, err error) {
	if dryRun {
		return false, nil
	}
	if yes {
		return true, nil
	}
	if interactive() {
		if err := confirm(ctx, title, summary); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, &cliError{code: ExitUsage, flag: "--yes",
		err: fmt.Errorf("%s writes to Proxmox: pass --yes to run it or --dry-run to see the plan", title)}
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
