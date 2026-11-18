package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// waitTask follows a task: in interactive mode with a spinner, a progress
// bar (from the "(x%)" of the task log) and the last log line; otherwise
// the log lines go to stderr.
func waitTask(ctx context.Context, c *pve.Client, title, upid string) (pve.TaskStatus, error) {
	if interactive() && stderrTTY() {
		return waitTaskUI(ctx, title, func(ctx context.Context, on func(pve.TaskEvent)) (pve.TaskStatus, error) {
			return c.WaitTask(ctx, upid, pve.WaitOptions{OnEvent: on})
		})
	}
	logf("%s: %s", title, upid)
	st, err := c.WaitTask(ctx, upid, pve.WaitOptions{OnEvent: func(ev pve.TaskEvent) { streamLine(ev.Line) }})
	endStream()
	return st, err
}

// overwriting is set while the last stderr line is a progress line that the
// next one may overwrite.
var overwriting bool

// streamLine writes a task log line to stderr. On a TTY, consecutive
// progress lines ("... (12.5%)") overwrite each other instead of scrolling.
func streamLine(line string) {
	if line == "" {
		return
	}
	_, isProgress := pve.ParsePercent(line)
	if !stderrTTY() {
		fmt.Fprintln(os.Stderr, "  │ "+line)
		return
	}
	if overwriting {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if isProgress {
		fmt.Fprint(os.Stderr, dimStyle.Render("  │ "+line))
		overwriting = true
		return
	}
	fmt.Fprintln(os.Stderr, dimStyle.Render("  │ "+line))
	overwriting = false
}

// endStream finishes a pending progress line.
func endStream() {
	if overwriting {
		fmt.Fprintln(os.Stderr)
		overwriting = false
	}
}

type taskEventMsg pve.TaskEvent
type taskDoneMsg struct {
	st  pve.TaskStatus
	err error
}

type taskModel struct {
	title   string
	spin    spinner.Model
	bar     progress.Model
	percent float64
	last    string
	lines   []string
	start   time.Time
	done    *taskDoneMsg
}

func (m taskModel) Init() tea.Cmd { return m.spin.Tick }

func (m taskModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case taskEventMsg:
		if msg.Percent >= 0 {
			m.percent = msg.Percent / 100
		}
		if strings.TrimSpace(msg.Line) != "" {
			m.last = msg.Line
			m.lines = append(m.lines, msg.Line)
		}
		return m, nil
	case taskDoneMsg:
		m.done = &msg
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.done = &taskDoneMsg{err: errDetached}
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

var errDetached = fmt.Errorf("detached; the task keeps running in Proxmox: %w", errAborted)

func (m taskModel) View() string {
	if m.done != nil {
		return ""
	}
	elapsed := time.Since(m.start).Truncate(time.Second)
	s := fmt.Sprintf("%s %s %s\n", m.spin.View(), titleStyle.Render(m.title), dimStyle.Render(elapsed.String()))
	if m.percent > 0 {
		s += "  " + m.bar.ViewAs(m.percent) + "\n"
	}
	last := m.last
	if len(last) > 100 {
		last = last[:100] + "…"
	}
	s += "  " + dimStyle.Render(last) + "\n" + dimStyle.Render("  ctrl+c detaches (the task keeps running)") + "\n"
	return s
}

func waitTaskUI(ctx context.Context, title string, run func(context.Context, func(pve.TaskEvent)) (pve.TaskStatus, error)) (pve.TaskStatus, error) {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = titleStyle
	m := taskModel{title: title, spin: sp, bar: progress.New(progress.WithDefaultGradient(), progress.WithWidth(50)), start: time.Now()}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr), tea.WithContext(ctx))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		st, err := run(ctx, func(ev pve.TaskEvent) { p.Send(taskEventMsg(ev)) })
		p.Send(taskDoneMsg{st: st, err: err})
	}()
	final, err := p.Run()
	if err != nil {
		return pve.TaskStatus{}, err
	}
	fm := final.(taskModel)
	if fm.done == nil {
		return pve.TaskStatus{}, errAborted
	}
	// keep the tail of the log on screen
	tail := fm.lines
	if len(tail) > 6 {
		tail = tail[len(tail)-6:]
	}
	for _, l := range tail {
		fmt.Fprintln(os.Stderr, dimStyle.Render("  │ "+l))
	}
	if fm.done.err == nil {
		fmt.Fprintf(os.Stderr, "%s %s (%s)\n", mark(true), title, time.Since(fm.start).Truncate(time.Second))
	} else {
		fmt.Fprintf(os.Stderr, "%s %s\n", mark(false), title)
	}
	return fm.done.st, fm.done.err
}
