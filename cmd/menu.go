package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/pvectl/internal/buildinfo"
)

// menuItem is one entry of the start menu: a command run with no
// arguments, so it asks for what it needs.
type menuItem struct {
	key   string
	label string
	desc  string
	write bool
	run   func(cmd *cobra.Command) error
}

func runSub(c *cobra.Command, args ...string) func(*cobra.Command) error {
	return func(parent *cobra.Command) error {
		c.SetContext(parent.Context())
		return c.RunE(c, args)
	}
}

func menuItems() []menuItem {
	return []menuItem{
		{key: "vms", label: "VMs", desc: "find VMs by name, with IP and recent tasks", run: func(cmd *cobra.Command) error {
			filter := ""
			in := huh.NewInput().Title("Name or VMID").Description("empty lists all VMs").Placeholder("platform-sthings").Value(&filter)
			if err := runForm(cmd.Context(), in); err != nil {
				return err
			}
			return runSub(vmsCmd, nonEmpty(strings.TrimSpace(filter))...)(cmd)
		}},
		{key: "templates", label: "Templates", desc: "all templates, per node", run: func(cmd *cobra.Command) error {
			vmsFlags.templates = true
			return runSub(vmsCmd)(cmd)
		}},
		{key: "config", label: "Config + checks", desc: "config of a VM or template, template checks", run: runSub(configCmd)},
		{key: "tasks", label: "Tasks", desc: "running and recent Proxmox tasks", run: runSub(tasksCmd)},
		{key: "ha", label: "HA status", desc: "HA state and last HA log of a VM", run: runSub(haCmd)},
		{key: "template promote", label: "Promote template", desc: "checks → migrate → FS9100 copy → repins", write: true, run: runSub(promoteCmd)},
		{key: "migrate", label: "Migrate", desc: "move a VM or template to another node", write: true, run: runSub(migrateCmd)},
		{key: "start", label: "Start", desc: "start a VM (HA-aware) and wait for its IP", write: true, run: runSub(startCmd)},
		{key: "ha-recover", label: "HA recover", desc: "get a VM out of HA error state", write: true, run: runSub(haRecoverCmd)},
	}
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// runMenu is `pvectl` on a terminal: the banner and a menu of the commands.
func runMenu(cmd *cobra.Command) error {
	fmt.Println(banner(buildinfo.Current(version, commit, buildDate).Version))
	items := menuItems()
	opts := make([]huh.Option[int], len(items))
	readTag := lipgloss.NewStyle().Foreground(colMint).Render("read ")
	writeTag := lipgloss.NewStyle().Foreground(colPink).Render("write")
	for i, it := range items {
		tag := readTag
		if it.write {
			tag = writeTag
		}
		opts[i] = huh.NewOption(fmt.Sprintf("%s  %-17s %s", tag, it.label, dimStyle.Render(it.desc)), i)
	}
	choice := 0
	sel := huh.NewSelect[int]().
		Title("What do you want to do?").
		Description("↑/↓ to move, enter to pick, / to filter, esc to quit · write commands confirm first").
		Options(opts...).Value(&choice)
	if err := runForm(cmd.Context(), sel); err != nil {
		return err
	}
	it := items[choice]
	fmt.Println(dimStyle.Render("  → pvectl " + it.key))
	return it.run(cmd)
}

// help: the cobra templates with styled headings, command and flag names.
// On a pipe the funcs return cobra's plain text.

var flagName = regexp.MustCompile(`^(\s+)((?:-\w, )?--[\w-]+)( \w+)?`)

func init() {
	cobra.AddTemplateFunc("h", func(s string) string {
		s = strings.TrimSuffix(s, ":")
		if !fancy() {
			return s + ":"
		}
		// "Read (safe to run any time)" -> pill READ + dim note
		name, note, _ := strings.Cut(s, " (")
		out := pill(strings.ToUpper(name), colPurple)
		if note != "" {
			out += " " + dimStyle.Render("("+note)
		}
		return out
	})
	cobra.AddTemplateFunc("cname", func(s string) string {
		if !fancy() {
			return s
		}
		return lipgloss.NewStyle().Foreground(colPink).Bold(true).Render(s)
	})
	cobra.AddTemplateFunc("styleFlags", func(s string) string {
		if !fancy() {
			return s
		}
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			if m := flagName.FindStringSubmatchIndex(l); m != nil {
				typ := ""
				if m[6] >= 0 {
					typ = dimStyle.Render(l[m[6]:m[7]])
				}
				lines[i] = l[m[2]:m[3]] + keyStyle.Bold(true).Render(l[m[4]:m[5]]) + typ + l[m[1]:]
			}
		}
		return strings.Join(lines, "\n")
	})
	cobra.AddTemplateFunc("logo", func(c *cobra.Command) string {
		if !fancy() || c.HasParent() {
			return ""
		}
		return banner(buildinfo.Current(version, commit, buildDate).Version) + "\n"
	})
	rootCmd.SetHelpTemplate(`{{logo .}}{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`)
	rootCmd.SetUsageTemplate(`{{h "Usage"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

{{h "Aliases"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{h "Examples"}}
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{h "Available Commands"}}{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{cname (rpad .Name .NamePadding) }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{h .Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{cname (rpad .Name .NamePadding) }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{h "Additional Commands"}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{cname (rpad .Name .NamePadding) }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{h "Flags"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces | styleFlags}}{{end}}{{if .HasAvailableInheritedFlags}}

{{h "Global Flags"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces | styleFlags}}{{end}}{{if .HasHelpSubCommands}}

{{h "Additional help topics"}}{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.HelpCommand}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)
}
