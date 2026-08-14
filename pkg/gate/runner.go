package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner invokes a command-line tool as a subprocess.
//
// It is deliberately thin. Everything CLI-specific — which flags to force,
// how to read a failure — is supplied by the caller, because those differ per
// tool and guessing produces silent misbehaviour. Gumroad's CLI has a uniform
// `--json` and a machine-readable error envelope; most do not.
type Runner struct {
	// Bin is the executable. A bare name is resolved on PATH.
	Bin string

	// BaseFlags are appended to every invocation, e.g. forcing structured
	// output and disabling interactive prompts. A tool with no such flags
	// leaves this empty and parses whatever it emits.
	BaseFlags []string

	// ConfirmFlag is appended once the gate has passed, for tools that would
	// otherwise prompt. Empty when the tool does not prompt.
	//
	// It is never appended to a preview: a preview has nothing to confirm, and
	// adding it would obscure what actually ran.
	ConfirmFlag string

	// Timeout bounds one invocation. Zero means no timeout.
	Timeout time.Duration

	// ParseError turns a failed invocation into an error. Return nil to fall
	// back to the default, which reports the exit code with whatever the tool
	// wrote. Use it to lift a structured error envelope into something the
	// caller can inspect.
	ParseError func(exitCode int, stdout, stderr []byte) error
}

// ExitError is the default failure when ParseError declines to interpret one.
type ExitError struct {
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExitError) Error() string {
	detail := strings.TrimSpace(e.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(e.Stdout)
	}
	if detail == "" {
		return fmt.Sprintf("%s: exit %d", strings.Join(e.Args, " "), e.ExitCode)
	}
	return fmt.Sprintf("%s: %s", strings.Join(e.Args, " "), truncate(detail, 400))
}

// Run invokes the tool with BaseFlags appended and returns its stdout.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.RunTimeout(ctx, r.Timeout, args...)
}

// RunTimeout is Run with an explicit deadline, for calls such as uploads that
// legitimately outlast the default.
func (r *Runner) RunTimeout(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	full := append(append([]string{}, args...), r.BaseFlags...)
	cmd := exec.CommandContext(ctx, r.bin(), full...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	runErr := cmd.Run()
	out := bytes.TrimSpace(stdout.Bytes())

	if runErr != nil {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s: timed out after %s", strings.Join(args, " "), timeout)
		}
		// Many tools print their error envelope on stdout and still exit
		// non-zero, so hand the parser both streams.
		if r.ParseError != nil {
			if err := r.ParseError(code, out, stderr.Bytes()); err != nil {
				return nil, err
			}
		}
		return nil, &ExitError{
			Args: args, ExitCode: code,
			Stdout: string(out), Stderr: stderr.String(),
		}
	}
	return out, nil
}

// Text invokes the tool without BaseFlags, for output meant for a human:
// `--help`, `--version`, or an embedded documentation command.
func (r *Runner) Text(ctx context.Context, args ...string) (string, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, r.bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return "", &ExitError{
			Args: args, ExitCode: code,
			Stdout: stdout.String(), Stderr: stderr.String(),
		}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Lookup reports whether the tool is installed, returning its resolved path.
func (r *Runner) Lookup() (string, error) {
	return exec.LookPath(r.bin())
}

func (r *Runner) bin() string { return r.Bin }

// Args assembles the argv for an allowed Decision, appending ConfirmFlag when
// the tier earned it. Previews are never auto-confirmed.
func (r *Runner) Args(d Decision, argv []string) []string {
	out := append([]string{}, argv...)
	if r.ConfirmFlag == "" || d.Preview || d.Tier == Read {
		return out
	}
	return append(out, r.ConfirmFlag)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
