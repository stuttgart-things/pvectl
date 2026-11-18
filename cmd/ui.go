package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// Styles, in the palette of stuttgart-things/claims.
var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4"))
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#04B575"))
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C")).Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#767676"))
	boxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#7D56F4")).Padding(0, 1)
)

// printSection prints a section header after one blank line.
func printSection(w io.Writer, title string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, sectionStyle.Render("━━━ "+title+" ━━━"))
}

func checkMark(c pve.Check) string {
	if c.Warn {
		return warnStyle.Render("!")
	}
	return mark(c.OK)
}

func mark(ok bool) string {
	if ok {
		return okStyle.Render("✓")
	}
	return errorStyle.Render("✗")
}

// table renders rows with columns padded to the widest cell (no colors in
// cells, so widths stay right).
func table(w io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	line := func(cells []string, style *lipgloss.Style) {
		parts := make([]string, len(cells))
		for i, c := range cells {
			if i == len(cells)-1 {
				parts[i] = c
			} else {
				parts[i] = c + strings.Repeat(" ", widths[i]-len(c))
			}
		}
		s := strings.TrimRight(strings.Join(parts, "  "), " ")
		if style != nil {
			s = style.Render(s)
		}
		fmt.Fprintln(w, s)
	}
	line(header, &keyStyle)
	for _, r := range rows {
		line(r, nil)
	}
}

func fmtTime(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04") + "Z"
}

func fmtUptime(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return fmt.Sprintf("%dh%02dm", sec/3600, (sec%3600)/60)
}
