// Package errs carries errors that tell the user what to do about them.
//
// The tool's whole value is turning Docker's undifferentiated failures into a
// specific diagnosis, so an error that says only "permission denied" is a bug.
// Every error a user can hit carries a stable Code and, where a precondition
// failed, a runnable Fix.
package errs

import (
	"errors"
	"fmt"
	"strings"
)

// Code identifies a failure. Codes are stable and documented: tests assert on
// them, never on prose, so wording stays free to improve.
type Code string

const (
	// Host / architecture
	CodeArchUnsupported Code = "E_ARCH_UNSUPPORTED"

	// Docker
	CodeDockerAbsent         Code = "E_DOCKER_ABSENT"
	CodeDockerDaemon         Code = "E_DOCKER_DAEMON"
	CodeDockerPermission     Code = "E_DOCKER_PERMISSION"
	CodeDockerStaleSession   Code = "E_DOCKER_STALE_SESSION"
	CodeDockerUnknown        Code = "E_DOCKER_UNKNOWN"
	CodeImageMissingManifest Code = "E_IMAGE_NO_ARM64"
	CodeImageNotROS          Code = "E_IMAGE_NOT_ROS"
	CodeDistroMismatch       Code = "E_DISTRO_MISMATCH"

	// Workspace / config
	CodeNoWorkspace   Code = "E_NO_WORKSPACE"
	CodeConfigInvalid Code = "E_CONFIG_INVALID"
	CodeWorkspaceInit Code = "E_WORKSPACE_EXISTS"

	// Container lifecycle
	CodePlanStale Code = "E_PLAN_STALE"

	// Hardware preconditions
	CodeI2CNotEnabled Code = "E_I2C_NOT_ENABLED"
	CodeSPINotEnabled Code = "E_SPI_NOT_ENABLED"
	CodeNoGPIOChip    Code = "E_NO_GPIO_CHIP"
	CodeNotInGroup    Code = "E_NOT_IN_GROUP"
)

// Step is one action in a Fix. Cmd is runnable verbatim when non-empty.
type Step struct {
	Text string
	Cmd  string
}

// Fix is what the user should do. A precondition error without one is a bug:
// telling someone their I2C is off without telling them how to turn it on just
// moves the problem.
type Fix struct {
	Steps       []Step
	NeedsReboot bool
	NeedsRoot   bool
}

// Actionable is an error that knows what it is and how to resolve it.
type Actionable struct {
	Code    Code
	Summary string   // one line, lowercase, no trailing period
	Detail  []string // observed values, not generic prose
	Fix     *Fix
	DocURL  string
	Cause   error
}

func (e *Actionable) Error() string { return string(e.Code) + ": " + e.Summary }

func (e *Actionable) Unwrap() error { return e.Cause }

// Is reports whether err is an Actionable with the given code, anywhere in the
// chain. Tests and callers branch on this rather than on message text.
func Is(err error, c Code) bool {
	var a *Actionable
	if errors.As(err, &a) {
		return a.Code == c
	}
	return false
}

// New builds an Actionable.
func New(c Code, summary string) *Actionable {
	return &Actionable{Code: c, Summary: summary}
}

func (e *Actionable) WithDetail(format string, args ...any) *Actionable {
	e.Detail = append(e.Detail, fmt.Sprintf(format, args...))
	return e
}

func (e *Actionable) WithFix(f *Fix) *Actionable {
	e.Fix = f
	return e
}

func (e *Actionable) WithCause(err error) *Actionable {
	e.Cause = err
	return e
}

func (e *Actionable) WithDoc(url string) *Actionable {
	e.DocURL = url
	return e
}

// Render formats the error for a terminal. Deliberately verbose: this is the
// moment a user is stuck, and the cost of extra lines is far lower than the cost
// of them guessing.
func (e *Actionable) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Error: %s", e.Summary)
	fmt.Fprintf(&b, "  [%s]\n", e.Code)

	if len(e.Detail) > 0 {
		b.WriteString("\n")
		for _, d := range e.Detail {
			fmt.Fprintf(&b, "  %s\n", d)
		}
	}

	if e.Fix != nil && len(e.Fix.Steps) > 0 {
		b.WriteString("\n  Fix:\n")
		for i, s := range e.Fix.Steps {
			if s.Text != "" {
				fmt.Fprintf(&b, "    %d. %s\n", i+1, s.Text)
			}
			if s.Cmd != "" {
				indent := "       "
				if s.Text == "" {
					fmt.Fprintf(&b, "    %d. %s\n", i+1, s.Cmd)
				} else {
					fmt.Fprintf(&b, "%s%s\n", indent, s.Cmd)
				}
			}
		}
		if e.Fix.NeedsReboot {
			b.WriteString("\n  This needs a reboot: it changes firmware settings that are read at boot.\n")
		}
	}

	if e.DocURL != "" {
		fmt.Fprintf(&b, "\n  More: %s\n", e.DocURL)
	}
	return b.String()
}
