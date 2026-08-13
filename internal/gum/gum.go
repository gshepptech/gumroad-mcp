// Package gum runs the official Gumroad CLI as a subprocess and hands back its
// JSON.
//
// gloop wraps `gumroad` rather than reimplementing the API: the official CLI
// already covers more of the surface than a second client reasonably could, and
// tracks it as Gumroad ships changes. What gloop adds on top is the tier policy
// in policy.go, which decides what an AI agent is allowed to invoke.
package gum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Binary is the executable gloop shells out to.
const Binary = "gumroad"

// baseFlags are forced onto every invocation.
//
//   - --json      structured output instead of the human tables
//   - --no-input  never block on an interactive prompt; there is no terminal here
//   - --quiet     no spinners or status chatter polluting stdout
var baseFlags = []string{"--json", "--no-input", "--quiet"}

// Runner invokes the Gumroad CLI.
type Runner struct {
	// Path is the resolved binary. Empty means "look it up on PATH".
	Path string
	// Timeout bounds a single invocation. Uploads get their own, longer bound.
	Timeout time.Duration
}

// New returns a Runner, or an error naming the install command when the
// official CLI is not present.
func New() (*Runner, error) {
	path, err := exec.LookPath(Binary)
	if err != nil {
		return nil, fmt.Errorf(
			"the Gumroad CLI is not installed — gloop wraps it.\n" +
				"  install: brew install antiwork/cli/gumroad\n" +
				"       or: curl -fsSL https://gumroad.com/install-cli.sh | bash")
	}
	return &Runner{Path: path, Timeout: 2 * time.Minute}, nil
}

// Error is a failed invocation. The Gumroad CLI reports failures as a JSON
// envelope on stdout with exit status 1, so the structured detail survives.
type Error struct {
	Args     []string
	ExitCode int
	Type     string // e.g. auth_error, api_error
	Code     string // e.g. not_authenticated
	Message  string
	Status   int // upstream HTTP status, when the CLI reports one
	Raw      string
}

func (e *Error) Error() string {
	if e.Message != "" {
		if e.Code != "" {
			return fmt.Sprintf("gumroad %s: %s (%s)", strings.Join(e.Args, " "), e.Message, e.Code)
		}
		return fmt.Sprintf("gumroad %s: %s", strings.Join(e.Args, " "), e.Message)
	}
	return fmt.Sprintf("gumroad %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, trunc(e.Raw, 300))
}

// NeedsAuth reports whether the failure is a missing or rejected credential,
// which the caller should surface as "log in" rather than "try again".
func (e *Error) NeedsAuth() bool {
	return e.Type == "auth_error" || e.Code == "not_authenticated" || e.Status == 401
}

// errEnvelope is the CLI's failure shape.
type errEnvelope struct {
	Success bool `json:"success"`
	Error   struct {
		Type       string `json:"type"`
		Code       string `json:"code"`
		Message    string `json:"message"`
		StatusCode int    `json:"status_code"`
	} `json:"error"`
}

// Run invokes the CLI and returns its raw JSON. baseFlags are appended, so
// callers pass only the command and its own flags.
func (r *Runner) Run(ctx context.Context, args ...string) (json.RawMessage, error) {
	return r.run(ctx, r.Timeout, args)
}

// RunLong is Run with a longer deadline, for uploads and exports.
func (r *Runner) RunLong(ctx context.Context, timeout time.Duration, args ...string) (json.RawMessage, error) {
	return r.run(ctx, timeout, args)
}

func (r *Runner) run(ctx context.Context, timeout time.Duration, args []string) (json.RawMessage, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	full := append(append([]string{}, args...), baseFlags...)
	cmd := exec.CommandContext(ctx, r.bin(), full...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	out := bytes.TrimSpace(stdout.Bytes())

	// A failure still prints its JSON envelope on stdout, so parse before
	// deciding what went wrong.
	if runErr != nil {
		e := &Error{Args: args, Raw: string(out), ExitCode: cmd.ProcessState.ExitCode()}
		var env errEnvelope
		if json.Unmarshal(out, &env) == nil && env.Error.Message != "" {
			e.Type, e.Code = env.Error.Type, env.Error.Code
			e.Message, e.Status = env.Error.Message, env.Error.StatusCode
		} else if s := strings.TrimSpace(stderr.String()); s != "" {
			e.Message = s
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			e.Message = fmt.Sprintf("timed out after %s", timeout)
		}
		return nil, e
	}

	if len(out) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(out) {
		return nil, &Error{
			Args: args, Raw: string(out),
			Message: "the Gumroad CLI returned output that is not JSON — check its version",
		}
	}
	return json.RawMessage(out), nil
}

// Text runs a command that is not asked for JSON, e.g. `auth token` or `--help`.
func (r *Runner) Text(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.bin(), append(args, "--no-input")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", &Error{Args: args, ExitCode: cmd.ProcessState.ExitCode(), Message: msg, Raw: msg}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Token returns the seller token the official CLI resolved, so gloop's own
// handful of native API calls authenticate as the same account without keeping
// a second credential on disk.
func (r *Runner) Token(ctx context.Context) (string, error) {
	tok, err := r.Text(ctx, "auth", "token")
	if err != nil {
		return "", fmt.Errorf("could not read the Gumroad CLI's token (run `gumroad auth login`): %w", err)
	}
	if tok == "" {
		return "", errors.New("the Gumroad CLI has no token — run `gumroad auth login`")
	}
	return tok, nil
}

// Skill returns the SKILL.md the official CLI embeds. It is the reference
// documentation for every command gloop proxies, so agents read it rather than
// relying on gloop to restate it.
func (r *Runner) Skill(ctx context.Context) (string, error) {
	return r.Text(ctx, "skill")
}

// Version reports the CLI version gloop is driving.
func (r *Runner) Version(ctx context.Context) (string, error) {
	out, err := r.Text(ctx, "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(out, "gumroad version "), nil
}

func (r *Runner) bin() string {
	if r.Path != "" {
		return r.Path
	}
	return Binary
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
