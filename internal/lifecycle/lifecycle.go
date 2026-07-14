// Package lifecycle manages the long-lived workspace container.
//
// There is no state file. Truth lives in the container's own docker labels, so
// nothing can drift out of sync with reality: if the container says it was
// built for plan X, it was.
package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/docker"
	"github.com/artineering/ros2pi/internal/dockerargs"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

// State is what docker currently knows about the workspace container.
type State struct {
	Exists  bool
	Running bool
	Plan    string // io.ros2pi.plan label
	Image   string // io.ros2pi.image label
}

// Manager drives the container for one workspace.
type Manager struct {
	Cfg     config.Config
	Facts   hostfacts.HostFacts
	Docker  docker.Client
	Version string
	ShimDir string // host directory holding entry.sh, bind-mounted read-only
}

// Inspect reports the container's current state.
func (m Manager) Inspect(ctx context.Context) (State, error) {
	p := dockerargs.InspectArgs(m.Cfg)
	r, err := m.Docker.Run(ctx, p.Args...)
	if err != nil {
		return State{}, err
	}
	if r.Code != 0 {
		// `docker inspect` on a missing container is not an error condition:
		// "no container" is the normal state before the first `up`.
		if strings.Contains(strings.ToLower(r.Stderr), "no such object") {
			return State{Exists: false}, nil
		}
		return State{}, docker.Diagnose(nil, r.Stderr)
	}

	parts := strings.Split(strings.TrimSpace(r.Stdout), "|")
	s := State{Exists: true}
	if len(parts) > 0 {
		s.Running = parts[0] == "running"
	}
	if len(parts) > 1 {
		s.Plan = parts[1]
	}
	if len(parts) > 2 {
		s.Image = parts[2]
	}
	return s, nil
}

// Ensure brings the workspace container up and ready to exec into.
//
// The state machine:
//
//	absent                -> create + start
//	exited, plan matches  -> start
//	exited, plan stale    -> remove + create + start
//	running, plan matches -> use
//	running, plan stale   -> refuse (see below)
//
// A stale RUNNING container is refused rather than silently recreated: it may
// own devices, hold a DDS graph, or be part-way through recording a bag.
// Killing that to apply a config edit the user made minutes ago would be a
// nasty surprise, so the decision is handed back to them.
func (m Manager) Ensure(ctx context.Context, recreate bool) (dockerargs.Plan, error) {
	want, err := dockerargs.CreateArgs(m.Cfg, m.Facts, m.Version)
	if err != nil {
		return want, err
	}

	st, err := m.Inspect(ctx)
	if err != nil {
		return want, err
	}

	switch {
	case !st.Exists:
		return want, m.createAndStart(ctx, want)

	case st.Plan == want.Fingerprint && !recreate:
		if st.Running {
			return want, nil
		}
		return want, m.start(ctx)

	case st.Running && !recreate:
		return want, stalePlanError(m.Cfg, st, want)

	default:
		if err := m.remove(ctx); err != nil {
			return want, err
		}
		return want, m.createAndStart(ctx, want)
	}
}

func (m Manager) createAndStart(ctx context.Context, p dockerargs.Plan) error {
	args := p.Args
	// The shim is mounted read-only at create time. It lives outside the plan
	// fingerprint because its path is content-addressed: a new ros2pi version
	// changes the path, and that SHOULD force a recreate, which it does by
	// changing these args.
	if m.ShimDir != "" {
		args = insertBeforeImage(args, p.Image,
			"-v", m.ShimDir+":"+dockerargs.ShimDir+":ro")
	}

	r, err := m.Docker.Run(ctx, args...)
	if err != nil {
		return err
	}
	if r.Code != 0 {
		return docker.Diagnose(nil, r.Stderr)
	}
	return m.start(ctx)
}

// insertBeforeImage places flags after the last flag and before the image name,
// which docker requires: everything after the image is the container's command.
func insertBeforeImage(args []string, image string, extra ...string) []string {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i] == image {
			out := make([]string, 0, len(args)+len(extra))
			out = append(out, args[:i]...)
			out = append(out, extra...)
			out = append(out, args[i:]...)
			return out
		}
	}
	return append(args, extra...) // image not found; caller will fail loudly
}

func (m Manager) start(ctx context.Context) error {
	p := dockerargs.StartArgs(m.Cfg)
	r, err := m.Docker.Run(ctx, p.Args...)
	if err != nil {
		return err
	}
	if r.Code != 0 {
		return docker.Diagnose(nil, r.Stderr)
	}
	return nil
}

func (m Manager) remove(ctx context.Context) error {
	p := dockerargs.RemoveArgs(m.Cfg)
	r, err := m.Docker.Run(ctx, p.Args...)
	if err != nil {
		return err
	}
	if r.Code != 0 && !strings.Contains(strings.ToLower(r.Stderr), "no such container") {
		return docker.Diagnose(nil, r.Stderr)
	}
	return nil
}

// Stop stops the container without removing it.
func (m Manager) Stop(ctx context.Context) error {
	p := dockerargs.StopArgs(m.Cfg)
	r, err := m.Docker.Run(ctx, p.Args...)
	if err != nil {
		return err
	}
	if r.Code != 0 && !strings.Contains(strings.ToLower(r.Stderr), "no such container") {
		return docker.Diagnose(nil, r.Stderr)
	}
	return nil
}

// Remove stops and deletes the container.
func (m Manager) Remove(ctx context.Context) error { return m.remove(ctx) }

// stalePlanError explains what changed, so the user can judge whether it is
// worth interrupting whatever the container is doing.
func stalePlanError(cfg config.Config, st State, want dockerargs.Plan) error {
	e := errs.New(errs.CodePlanStale,
		"the workspace container is running with an outdated configuration").
		WithDetail("container: %s", want.Container).
		WithDetail("running:   %s", short(st.Plan)).
		WithDetail("config:    %s", short(want.Fingerprint))

	if st.Image != "" && st.Image != want.Image {
		e.WithDetail("image:     %s -> %s", st.Image, want.Image)
	}
	e.WithDetail("")
	e.WithDetail("ros2pi.toml has changed since this container was created.")
	e.WithDetail("Recreating it will stop anything running inside -- nodes, a bag")
	e.WithDetail("recording, an open shell -- so it is not done automatically.")

	return e.WithFix(&errs.Fix{Steps: []errs.Step{
		{Text: "apply the new configuration (stops running nodes):",
			Cmd: "ros2pi up --recreate"},
	}})
}

func short(fp string) string {
	if fp == "" {
		return "(none)"
	}
	if len(fp) > 23 {
		return fp[:23]
	}
	return fp
}

// Describe renders a plan's reasoning, for --explain and --dry-run.
func Describe(p dockerargs.Plan) string {
	var b strings.Builder
	for _, d := range p.Decisions {
		if d.Source != "" {
			fmt.Fprintf(&b, "  %-28s %s  [%s]\n", d.Arg, d.Reason, d.Source)
		} else {
			fmt.Fprintf(&b, "  %-28s %s\n", d.Arg, d.Reason)
		}
	}
	return b.String()
}
