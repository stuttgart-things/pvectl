package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	ltable "github.com/charmbracelet/lipgloss/table"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// printSection prints a section header after one blank line.
func printSection(w io.Writer, title string) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, heading(title))
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
	if fancy() {
		fancyTable(w, header, rows, nil)
		return
	}
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
		_, _ = fmt.Fprintln(w, s)
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

// fancyTable renders a rounded, colored table. cell may restyle a cell
// (row index into rows, column); it gets the plain value.
func fancyTable(w io.Writer, header []string, rows [][]string, cell func(row, col int, v string) string) {
	shown := rows
	if cell != nil {
		shown = make([][]string, len(rows))
		for i, r := range rows {
			shown[i] = make([]string, len(r))
			for j, v := range r {
				shown[i][j] = cell(i, j, v)
			}
		}
	}
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colPink).Padding(0, 1)
	even := lipgloss.NewStyle().Padding(0, 1).Foreground(colText)
	odd := even.Foreground(lipgloss.AdaptiveColor{Light: "#3A3A3A", Dark: "#BDBDBD"})
	t := ltable.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(colPurple)).
		Headers(header...).
		Rows(shown...).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == ltable.HeaderRow:
				return headerStyle
			case row%2 == 0:
				return even
			default:
				return odd
			}
		})
	_, _ = fmt.Fprintln(w, t.Render())
	_, _ = fmt.Fprintln(w, dimStyle.Render(fmt.Sprintf("  %d %s", len(rows), plural(len(rows), "entry", "entries"))))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
