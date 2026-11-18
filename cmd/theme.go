package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/mattn/go-isatty"
)

// Palette: the stuttgart-things purple, a pink accent, mint for "good".
var (
	colPurple = lipgloss.AdaptiveColor{Light: "#5A3FD9", Dark: "#7D56F4"}
	colPink   = lipgloss.AdaptiveColor{Light: "#D6336C", Dark: "#F25D94"}
	colMint   = lipgloss.AdaptiveColor{Light: "#02875A", Dark: "#04B575"}
	colAmber  = lipgloss.AdaptiveColor{Light: "#B35C00", Dark: "#FFB86C"}
	colRed    = lipgloss.AdaptiveColor{Light: "#C9184A", Dark: "#FF5F87"}
	colDim    = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#767676"}
	colText   = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#EEEEEE"}
	colCream  = lipgloss.Color("#FFFDF5")

	// logo gradient (dark backgrounds look best with the bright pair)
	gradFrom = "#7D56F4"
	gradTo   = "#F25D94"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(colPurple)
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(colPink)
	keyStyle     = lipgloss.NewStyle().Foreground(colPurple)
	okStyle      = lipgloss.NewStyle().Foreground(colMint).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(colAmber).Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(colDim)
	textStyle    = lipgloss.NewStyle().Foreground(colText)
	cardStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPurple).Padding(0, 2)
	warnBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAmber).Padding(0, 2)
	pillBase     = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(colCream)
)

// card renders content in style, wrapped to the terminal width when it
// would not fit.
func card(style lipgloss.Style, content string) string {
	limit := termWidth() - 1
	if lipgloss.Width(style.Render(content)) > limit {
		return style.Width(limit - style.GetHorizontalBorderSize()).Render(content)
	}
	return style.Render(content)
}

// cards renders several blocks with the same width, so stacked cards line up.
func cards(style lipgloss.Style, contents ...string) string {
	w := 0
	for _, c := range contents {
		if n := lipgloss.Width(c) + style.GetHorizontalPadding(); n > w {
			w = n
		}
	}
	if limit := termWidth() - 1 - style.GetHorizontalBorderSize(); w > limit {
		w = limit
	}
	out := make([]string, len(contents))
	for i, c := range contents {
		out[i] = style.Width(w).Render(c)
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

// pill renders a small colored badge like ` TEMPLATE `.
func pill(text string, bg lipgloss.TerminalColor) string {
	return pillBase.Background(bg).Render(text)
}

// fancyOverride lets tests render the TTY look into a pipe.
var fancyOverride bool

// fancy reports whether stdout gets the full look (cards, bordered tables,
// logo): text output on a terminal. Pipes keep the plain, grep-able format;
// -o json is never styled.
func fancy() bool {
	if jsonOut() {
		return false
	}
	return fancyOverride || isatty.IsTerminal(os.Stdout.Fd())
}

// termWidth is the width of stdout, 80 if unknown.
func termWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 80
}

// The logo in ANSI Shadow, one entry per letter so it can be colored by column.
var logoLetters = [][]string{
	{"██████╗ ", "██╔══██╗", "██████╔╝", "██╔═══╝ ", "██║     ", "╚═╝     "},
	{"██╗   ██╗", "██║   ██║", "██║   ██║", "╚██╗ ██╔╝", " ╚████╔╝ ", "  ╚═══╝  "},
	{"███████╗", "██╔════╝", "█████╗  ", "██╔══╝  ", "███████╗", "╚══════╝"},
	{" ██████╗", "██╔════╝", "██║     ", "██║     ", "╚██████╗", " ╚═════╝"},
	{"████████╗", "╚══██╔══╝", "   ██║   ", "   ██║   ", "   ██║   ", "   ╚═╝   "},
	{"██╗     ", "██║     ", "██║     ", "██║     ", "███████╗", "╚══════╝"},
}

// a compact version for narrow terminals and the help header
var logoSmall = []string{
	"┌─┐┬  ┬┌─┐┌─┐┌┬┐┬  ",
	"├─┘└┐┌┘├┤ │   │ │  ",
	"┴   └┘ └─┘└─┘ ┴ ┴─┘",
}

// gradient colors every rune of lines by its column, gradFrom -> gradTo.
func gradient(lines []string) string {
	width := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > width {
			width = n
		}
	}
	from, _ := colorful.Hex(gradFrom)
	to, _ := colorful.Hex(gradTo)
	cols := make([]lipgloss.Style, width)
	for i := range cols {
		t := 0.0
		if width > 1 {
			t = float64(i) / float64(width-1)
		}
		cols[i] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(from.BlendLuv(to, t).Clamped().Hex()))
	}
	out := make([]string, len(lines))
	for li, l := range lines {
		var b strings.Builder
		for i, r := range []rune(l) {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			b.WriteString(cols[i].Render(string(r)))
		}
		out[li] = b.String()
	}
	return strings.Join(out, "\n")
}

func bigLogo() []string {
	lines := make([]string, len(logoLetters[0]))
	for _, letter := range logoLetters {
		for i, part := range letter {
			lines[i] += part
		}
	}
	return lines
}

// banner is the logo plus a tagline and the version.
func banner(ver string) string {
	logo := bigLogo()
	if termWidth() < 60 {
		logo = logoSmall
	}
	tag := lipgloss.JoinHorizontal(lipgloss.Center,
		pill("LabUL", colPurple), " ",
		pill("Proxmox", colPink), " ",
		dimStyle.Render("templates · migrate · promote"),
	)
	if ver != "" {
		tag += "  " + dimStyle.Render(ver)
	}
	return lipgloss.NewStyle().Margin(1, 0, 1, 2).Render(gradient(logo) + "\n\n" + tag)
}

// formTheme is the huh theme in the pvectl palette.
func formTheme() *huh.Theme {
	t := huh.ThemeCharm()
	f := &t.Focused
	f.Base = f.Base.BorderForeground(colPurple)
	f.Card = f.Base
	f.Title = f.Title.Foreground(colPurple).Bold(true)
	f.NoteTitle = f.NoteTitle.Foreground(colPurple).Bold(true)
	f.Description = f.Description.Foreground(colDim)
	f.SelectSelector = f.SelectSelector.Foreground(colPink).SetString("▸ ")
	f.SelectedOption = f.SelectedOption.Foreground(colMint)
	f.Option = f.Option.Foreground(colText)
	f.NextIndicator = f.NextIndicator.Foreground(colPink)
	f.PrevIndicator = f.PrevIndicator.Foreground(colPink)
	f.FocusedButton = f.FocusedButton.Background(colPink).Foreground(colCream).Bold(true)
	f.BlurredButton = f.BlurredButton.Background(lipgloss.AdaptiveColor{Light: "#E4E4E4", Dark: "#3A3A3A"}).Foreground(colText)
	f.TextInput.Cursor = f.TextInput.Cursor.Foreground(colPink)
	f.TextInput.Prompt = f.TextInput.Prompt.Foreground(colPink)
	f.ErrorIndicator = f.ErrorIndicator.Foreground(colRed)
	f.ErrorMessage = f.ErrorMessage.Foreground(colRed)

	b := &t.Blurred
	*b = *f
	b.Base = b.Base.BorderStyle(lipgloss.HiddenBorder())
	b.Card = b.Base
	b.SelectSelector = b.SelectSelector.SetString("  ")
	b.NextIndicator = lipgloss.NewStyle()
	b.PrevIndicator = lipgloss.NewStyle()
	return t
}

// runForm runs one group of fields with the pvectl theme.
func runForm(ctx context.Context, fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(formTheme()).RunWithContext(ctx)
}

// heading prints a pill-style section heading (fancy) or the plain one.
func heading(title string) string {
	if fancy() {
		return pill(strings.ToUpper(title), colPurple)
	}
	return sectionStyle.Render("━━━ " + title + " ━━━")
}

// guestTitle is "<vmid> <name>" plus a kind badge and the node.
func guestTitle(vmid int, name, node string, template bool) string {
	if !fancy() {
		kind := "VM"
		if template {
			kind = "template"
		}
		return fmt.Sprintf("%s %s", titleStyle.Render(fmt.Sprintf("%d %s", vmid, name)), dimStyle.Render(fmt.Sprintf("(%s on %s)", kind, node)))
	}
	badge := pill("VM", colPurple)
	if template {
		badge = pill("TEMPLATE", colPink)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		badge, " ",
		titleStyle.Render(fmt.Sprintf("%d", vmid)), " ",
		textStyle.Bold(true).Render(name), " ",
		dimStyle.Render("@ "+node),
	)
}

// statusCell is a colored status with a dot, for the fancy table.
func statusCell(status string, template bool) string {
	switch {
	case template:
		return lipgloss.NewStyle().Foreground(colPink).Render("◆ template")
	case status == "running":
		return okStyle.Render("● running")
	case status == "stopped":
		return dimStyle.Render("○ stopped")
	default:
		return warnStyle.Render("◐ " + status)
	}
}
