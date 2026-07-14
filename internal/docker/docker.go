// Package docker runs the docker CLI and translates its failures.
//
// It shells out rather than using the SDK: the SDK is a large dependency for a
// tool whose whole job is assembling a command line, and shelling out means
// --dry-run output is exactly what runs.
package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/artineering/ros2pi/internal/errs"
)

// Client runs docker commands.
type Client struct {
	// Bin is the docker binary. Empty means "docker" on PATH.
	Bin string

	// DryRun prints the command instead of running it.
	DryRun bool

	// Verbose echoes every command before running it.
	Verbose bool
}

func (c Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "docker"
}

// Result is the outcome of a docker command.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Run executes a docker command and captures its output.
func (c Client) Run(ctx context.Context, args ...string) (Result, error) {
	if c.Verbose || c.DryRun {
		printCmd(c.bin(), args)
	}
	if c.DryRun {
		return Result{}, nil
	}

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	r := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.Code = ee.ExitCode()
	default:
		return r, Diagnose(err, "")
	}
	return r, nil
}

// Attach runs a docker command wired directly to this process's stdio, and
// returns the command's exit code.
//
// Used for exec and shell, where the point is that the user's terminal talks to
// the process inside the container. Output is NOT captured: buffering would
// break `ros2pi topic echo` and interactive prompts alike.
func (c Client) Attach(ctx context.Context, args ...string) (int, error) {
	if c.Verbose || c.DryRun {
		printCmd(c.bin(), args)
	}
	if c.DryRun {
		return 0, nil
	}

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Signals are forwarded by docker itself to the process in the container,
	// so this process must NOT die on Ctrl-C before docker has relayed it --
	// otherwise `ros2pi run` would leave the node running headless. Cancel is
	// left to the context, and SIGINT is ignored here so the child sees it.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// Propagate the command's own exit code verbatim: `ros2pi colcon build`
		// must fail a CI script exactly as `colcon build` would.
		return ee.ExitCode(), nil
	}
	return 1, Diagnose(err, "")
}

func printCmd(bin string, args []string) {
	var b strings.Builder
	b.WriteString("+ ")
	b.WriteString(bin)
	for _, a := range args {
		b.WriteByte(' ')
		if strings.ContainsAny(a, " \t\"'") {
			b.WriteString(strconv_Quote(a))
		} else {
			b.WriteString(a)
		}
	}
	os.Stderr.WriteString(b.String() + "\n")
}

// strconv_Quote is strconv.Quote, wrapped so the import stays local to display
// code and cannot creep into logic.
func strconv_Quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// Diagnose maps a docker failure onto an actionable error.
//
// Without this table every docker problem looks identical to a user: "it did
// not work". The distinctions here are the difference between `ros2pi setup`,
// `newgrp docker`, and `systemctl start docker` -- three different afternoons.
func Diagnose(err error, stderr string) error {
	s := strings.ToLower(stderr)

	switch {
	case errors.Is(err, exec.ErrNotFound):
		return errs.New(errs.CodeDockerAbsent, "docker is not installed").
			WithDetail("no `docker` binary was found on PATH").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "install it:", Cmd: "curl -fsSL https://get.docker.com | sh"},
				{Text: "then add yourself to the docker group:",
					Cmd: "sudo usermod -aG docker $USER && newgrp docker"},
			}})

	case strings.Contains(s, "permission denied") && strings.Contains(s, "docker"):
		// hostfacts distinguishes stale-session from genuine non-membership;
		// this path is the fallback when facts were not available.
		return errs.New(errs.CodeDockerPermission, "cannot talk to the docker daemon").
			WithDetail("permission denied on the docker socket").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "if you are not in the docker group:",
					Cmd: "sudo usermod -aG docker $USER"},
				{Text: "then start a new session (this does NOT need sudo):",
					Cmd: "newgrp docker"},
			}})

	case strings.Contains(s, "cannot connect to the docker daemon"),
		strings.Contains(s, "is the docker daemon running"):
		return errs.New(errs.CodeDockerDaemon, "the docker daemon is not running").
			WithFix(&errs.Fix{NeedsRoot: true, Steps: []errs.Step{
				{Cmd: "sudo systemctl start docker"},
				{Text: "and to start it at boot:", Cmd: "sudo systemctl enable docker"},
			}})

	case strings.Contains(s, "no matching manifest"):
		return errs.New(errs.CodeImageMissingManifest,
			"that image has no build for this architecture").
			WithDetail("%s", strings.TrimSpace(stderr)).
			WithDetail("ROS 2 images are published for arm64 and amd64 only.").
			WithDoc("https://github.com/artineering/ros2pi#arm64-only--and-that-is-not-our-choice")
	}

	if err != nil {
		return errs.New(errs.CodeDockerUnknown, "docker failed").
			WithDetail("%s", strings.TrimSpace(stderr)).
			WithCause(err)
	}
	return errs.New(errs.CodeDockerUnknown, "docker failed").
		WithDetail("%s", strings.TrimSpace(stderr))
}
