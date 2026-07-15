// Package check turns what ros2pi knows about a Pi into advice.
//
// Every check is a PURE function of (HostFacts, Config), like dockerargs and
// for the same reason: the interesting cases are hosts we do not have. A Pi 5
// on kernel 6.6, a rootless docker, a 32-bit userland, a Pi where i2c is
// half-configured -- all of them are a JSON fixture away, and none of them
// require hardware.
//
// The rule that shapes everything here: a check that reports a problem without
// saying what to do about it has not helped. "i2c is not enabled" is where most
// tools stop, and it is precisely the point at which the user is stuck.
package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
	"github.com/artineering/ros2pi/internal/imagefacts"
)

// Status is a check's verdict.
type Status string

const (
	// OK: works.
	OK Status = "ok"
	// Warn: works, but something will bite later.
	Warn Status = "warn"
	// Fail: broken, and ros2pi cannot do what was asked.
	Fail Status = "fail"
	// Note: neither good nor bad; context the user should have.
	Note Status = "note"
	// Skip: not applicable, or not asked for.
	Skip Status = "skip"
)

// Result is one check's outcome.
type Result struct {
	Status Status
	// Value is the short answer, shown on the right of the check's name.
	Value string
	// Detail explains, in the user's terms. Observed values, not prose.
	Detail []string
	// Fix is required whenever Status is Fail: see the package comment.
	Fix *errs.Fix
}

// Check is one question ros2pi can answer about a host.
type Check struct {
	// ID is stable and used by --explain. It never changes once published.
	ID      string
	Section string
	Title   string
	// Run is pure. cfg is nil when not inside a workspace, and img is nil when
	// there is no workspace or docker could not be asked: `ros2pi check` must
	// work anywhere, because a user whose setup is broken has nowhere else to
	// go. A check handed nil simply skips.
	Run func(f hostfacts.HostFacts, cfg *config.Config, img *imagefacts.Facts) Result
}

// Report is a whole run.
type Report struct {
	Sections []Section
	Failed   int
	Warned   int
}

type Section struct {
	Name    string
	Results []Entry
}

type Entry struct {
	Check  Check
	Result Result
}

// Run executes every check.
func Run(f hostfacts.HostFacts, cfg *config.Config, img *imagefacts.Facts) Report {
	var rep Report
	order := []string{}
	bySection := map[string][]Entry{}

	for _, c := range Registry() {
		r := c.Run(f, cfg, img)
		if r.Status == Skip {
			continue
		}
		if _, seen := bySection[c.Section]; !seen {
			order = append(order, c.Section)
		}
		bySection[c.Section] = append(bySection[c.Section], Entry{Check: c, Result: r})

		switch r.Status {
		case Fail:
			rep.Failed++
		case Warn:
			rep.Warned++
		}
	}

	for _, name := range order {
		rep.Sections = append(rep.Sections, Section{Name: name, Results: bySection[name]})
	}
	return rep
}

// Find returns a check by ID, for --explain.
func Find(id string) (Check, bool) {
	for _, c := range Registry() {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

// IDs lists every check ID, sorted.
func IDs() []string {
	var out []string
	for _, c := range Registry() {
		out = append(out, c.ID)
	}
	sort.Strings(out)
	return out
}

// Registry is every check, in report order.
func Registry() []Check {
	var all []Check
	all = append(all, hostChecks()...)
	all = append(all, dockerChecks()...)
	all = append(all, imageChecks()...)
	all = append(all, workspaceChecks()...)
	all = append(all, hardwareChecks()...)
	return all
}

// --- helpers shared by the check sets ---

func ok(value string) Result { return Result{Status: OK, Value: value} }
func note(value string, detail ...string) Result {
	return Result{Status: Note, Value: value, Detail: detail}
}

func warn(value string, detail ...string) Result {
	return Result{Status: Warn, Value: value, Detail: detail}
}

func fail(value string, detail ...string) Result {
	return Result{Status: Fail, Value: value, Detail: detail}
}

func (r Result) withFix(f *errs.Fix) Result {
	r.Fix = f
	return r
}

func (r Result) withDetail(format string, a ...any) Result {
	r.Detail = append(r.Detail, fmt.Sprintf(format, a...))
	return r
}

func joinPaths(ns []hostfacts.DevNode) string {
	var out []string
	for _, n := range ns {
		if n.Exists {
			out = append(out, n.Path)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
