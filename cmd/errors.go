package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/stuttgart-things/pvectl/pkg/promote"
	"github.com/stuttgart-things/pvectl/pkg/pve"
)

// Exit codes (README: "Exit codes").
const (
	ExitOK          = 0
	ExitError       = 1   // any other failure (API, network, TLS)
	ExitUsage       = 2   // bad or missing flags/arguments, missing env
	ExitNotFound    = 3   // VM, template, node or task not found
	ExitCheckFailed = 4   // config sanity check failed / source is no template
	ExitTaskFailed  = 5   // a Proxmox task ended with an error
	ExitAborted     = 130 // aborted at a prompt or by Ctrl+C
)

var kinds = map[int]string{
	ExitError: "error", ExitUsage: "usage", ExitNotFound: "not_found",
	ExitCheckFailed: "check_failed", ExitTaskFailed: "task_failed", ExitAborted: "aborted",
}

// cliError carries an exit code and, for usage errors, the missing flag.
type cliError struct {
	code   int
	flag   string
	err    error
	silent bool // the result is already on stdout; only the exit code is left
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

// silentErr ends a command whose result is already printed with code.
func silentErr(code int, err error) error { return &cliError{code: code, err: err, silent: true} }

func usageErr(format string, a ...any) error {
	return &cliError{code: ExitUsage, err: fmt.Errorf(format, a...)}
}

// missingFlag is the error for an input that has no value in
// non-interactive mode.
func missingFlag(flag, what string) error {
	return &cliError{code: ExitUsage, flag: flag,
		err: fmt.Errorf("missing %s: pass %s (no prompt without a TTY, with --non-interactive or -o json)", what, flag)}
}

var errAborted = errors.New("aborted")

// exitCode classifies an error.
func exitCode(err error) int {
	var ce *cliError
	var envErr *pve.MissingEnvError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, errAborted), errors.Is(err, huh.ErrUserAborted), errors.Is(err, context.Canceled):
		return ExitAborted
	case errors.As(err, &envErr):
		return ExitUsage
	case errors.Is(err, pve.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, promote.ErrChecks), errors.Is(err, promote.ErrNotTemplate), errors.Is(err, errChecks):
		return ExitCheckFailed
	case errors.Is(err, pve.ErrTaskFailed):
		return ExitTaskFailed
	}
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "invalid argument", "flag needs an argument",
		"accepts ", "requires at least", "required flag"} {
		if strings.HasPrefix(msg, p) || strings.Contains(msg, " "+p) {
			return ExitUsage
		}
	}
	return ExitError
}

// errChecks is returned by `config` when a sanity check fails.
var errChecks = errors.New("config checks failed")

// reportError prints err (JSON on stdout with -o json, else styled on
// stderr) and returns the exit code.
func reportError(err error) int {
	code := exitCode(err)
	if code == ExitOK {
		return code
	}
	var silent *cliError
	if errors.As(err, &silent) && silent.silent {
		if !jsonOut() {
			_, _ = fmt.Fprintln(os.Stderr, errorStyle.Render("✗ ")+err.Error())
		}
		return code
	}
	if opts.output == "json" {
		out := map[string]any{"error": err.Error(), "code": code, "kind": kinds[code]}
		var ce *cliError
		if errors.As(err, &ce) && ce.flag != "" {
			out["flag"] = ce.flag
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return code
	}
	if code == ExitAborted {
		if errors.Is(err, errDetached) {
			_, _ = fmt.Fprintln(os.Stderr, err.Error())
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "Aborted.")
		}
		return code
	}
	_, _ = fmt.Fprintln(os.Stderr, errorStyle.Render("Error: ")+err.Error())
	if code == ExitUsage {
		_, _ = fmt.Fprintln(os.Stderr, dimStyle.Render("Run 'pvectl <command> --help' for usage and examples."))
	}
	return code
}
