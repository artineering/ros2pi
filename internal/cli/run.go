package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/docker"
	"github.com/artineering/ros2pi/internal/dockerargs"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
	"github.com/artineering/ros2pi/internal/lifecycle"
	"github.com/artineering/ros2pi/internal/shim"
)

// App holds everything a command needs.
type App struct {
	Version string
	Stdin   *os.File
	Stdout  *os.File
	Stderr  *os.File

	// IsTTY reports whether both stdin and stdout are terminals. A TTY is
	// allocated only then: doing it when stdout is a pipe injects control
	// characters and breaks `ros2pi topic echo | head`.
	IsTTY func() bool
}

// Execute routes and runs argv, returning the process exit code.
func (a App) Execute(ctx context.Context, argv []string) int {
	in, err := Route(argv)
	if err != nil {
		fmt.Fprintln(a.Stderr, "ros2pi: "+err.Error())
		return 2
	}

	switch in.Mode {
	case ModeHelp:
		a.usage()
		return 0
	case ModeVersion:
		fmt.Fprintln(a.Stdout, "ros2pi "+a.Version)
		return 0
	}

	code, err := a.dispatch(ctx, in)
	if err != nil {
		a.renderError(err)
		return 1
	}
	return code
}

func (a App) renderError(err error) {
	var act *errs.Actionable
	if ok := asActionable(err, &act); ok {
		fmt.Fprint(a.Stderr, act.Render())
		return
	}
	fmt.Fprintln(a.Stderr, "ros2pi: "+err.Error())
}

func asActionable(err error, target **errs.Actionable) bool {
	for err != nil {
		if a, ok := err.(*errs.Actionable); ok {
			*target = a
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (a App) dispatch(ctx context.Context, in Invocation) (int, error) {
	// `init` and `setup` are the only commands that work without an existing
	// workspace, so they run before any config is loaded.
	switch in.Verb {
	case "init":
		return 0, a.cmdInit(in)
	case "setup":
		return 0, fmt.Errorf("`ros2pi setup` is not implemented yet; install docker with: curl -fsSL https://get.docker.com | sh")
	case "check":
		return a.cmdCheck(ctx, in)
	}

	env, err := a.load(ctx, in)
	if err != nil {
		return 1, err
	}

	switch in.Mode {
	case ModePassthrough:
		// The single place a ros2 command is adjusted before forwarding; see
		// adjustPassthrough for why this exception exists and how narrow it is.
		args, note := adjustPassthrough(in.PassArgs)
		if note != "" {
			// Never rewrite someone's command silently.
			fmt.Fprintln(a.Stderr, "ros2pi: "+note)
		}
		return env.exec(ctx, append([]string{"ros2"}, args...))
	}

	switch in.Verb {
	case "up":
		return 0, env.up(ctx, in.Globals.Recreate)
	case "down":
		return 0, env.down(ctx)
	case "build":
		return env.build(ctx, in.OwnArgs)
	case "shell":
		return env.exec(ctx, append([]string{"bash"}, in.OwnArgs...))
	case "image":
		return env.image(ctx, in.OwnArgs)
	}
	return 1, fmt.Errorf("unknown command %q", in.Verb)
}

// workspaceEnv is a loaded workspace, ready to act on.
type workspaceEnv struct {
	app   App
	cfg   config.Config
	facts hostfacts.HostFacts
	mgr   lifecycle.Manager
	in    Invocation
}

func (a App) load(ctx context.Context, in Invocation) (*workspaceEnv, error) {
	dir := in.Globals.Workspace
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = wd
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	facts, err := a.probe(ctx)
	if err != nil {
		return nil, err
	}

	if err := dockerUsable(facts); err != nil {
		return nil, err
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(os.TempDir(), "ros2pi")
	}
	shimDir, err := shim.Materialise(filepath.Join(cache, "ros2pi"))
	if err != nil {
		return nil, err
	}

	cl := docker.Client{DryRun: in.Globals.DryRun, Verbose: in.Globals.Verbose}
	return &workspaceEnv{
		app: a, cfg: cfg, facts: facts, in: in,
		mgr: lifecycle.Manager{
			Cfg: cfg, Facts: facts, Docker: cl,
			Version: a.Version, ShimDir: shimDir,
		},
	}, nil
}

func (a App) probe(ctx context.Context) (hostfacts.HostFacts, error) {
	if p := os.Getenv("ROS2PI_FACTS"); p != "" {
		pr, err := factsProber(p)
		if err != nil {
			return hostfacts.HostFacts{}, err
		}
		return pr.Probe(ctx)
	}
	return hostfacts.NewLinuxProber(hostfacts.NewOSHost(), a.Version).Probe(ctx)
}

// dockerUsable turns the probed docker classification into an actionable error.
// This is where "you are in the docker group but this shell predates it" earns
// its keep: the fix is the opposite of what the raw message suggests.
func dockerUsable(f hostfacts.HostFacts) error {
	switch f.Docker.Problem {
	case hostfacts.DockerOK:
		return nil

	case hostfacts.DockerAbsent:
		return errs.New(errs.CodeDockerAbsent, "docker is not installed").
			WithDetail("ros2pi runs ROS 2 in a container, so docker is required").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "install it:", Cmd: "curl -fsSL https://get.docker.com | sh"},
				{Text: "then:", Cmd: "sudo usermod -aG docker $USER && newgrp docker"},
			}})

	case hostfacts.DockerDaemonUnreachable:
		return errs.New(errs.CodeDockerDaemon, "the docker daemon is not running").
			WithFix(&errs.Fix{NeedsRoot: true, Steps: []errs.Step{
				{Cmd: "sudo systemctl start docker"},
				{Text: "start it at boot too:", Cmd: "sudo systemctl enable docker"},
			}})

	case hostfacts.DockerPermissionStaleSession:
		g, _ := f.Group("docker")
		return errs.New(errs.CodeDockerStaleSession,
			"you are in the docker group, but this shell started before you were added").
			WithDetail("/etc/group lists you in `docker` (gid %d)", g.GID).
			WithDetail("but this process's credentials do not include it").
			WithDetail("").
			WithDetail("Running `usermod` again will not help, and neither will sudo:").
			WithDetail("the group is already yours, this session just predates it.").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "start a shell that has the group:", Cmd: "newgrp docker"},
				{Text: "or log out and back in, which fixes it everywhere"},
			}})

	case hostfacts.DockerPermissionDenied:
		return errs.New(errs.CodeDockerPermission, "you are not in the docker group").
			WithDetail("talking to the docker socket needs membership of `docker`").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Cmd: "sudo usermod -aG docker $USER"},
				{Text: "then start a new session (no sudo needed):", Cmd: "newgrp docker"},
			}})
	}

	return errs.New(errs.CodeDockerUnknown, "docker is not usable").
		WithDetail("%s", f.Docker.Detail).
		WithFix(&errs.Fix{Steps: []errs.Step{
			{Text: "please report this, attaching:", Cmd: "ros2pi check --dump-facts"},
		}})
}

func (e *workspaceEnv) up(ctx context.Context, recreate bool) error {
	p, err := e.mgr.Ensure(ctx, recreate)
	if err != nil {
		return err
	}
	for _, w := range p.Warnings {
		fmt.Fprintln(e.app.Stderr, "warning: "+w)
	}
	fmt.Fprintf(e.app.Stdout, "%s is up (%s)\n", p.Container, e.cfg.ROS.Image)
	return nil
}

func (e *workspaceEnv) down(ctx context.Context) error {
	if err := e.mgr.Stop(ctx); err != nil {
		return err
	}
	fmt.Fprintf(e.app.Stdout, "%s stopped\n", dockerargs.ContainerName(e.cfg.Root))
	return nil
}

func (e *workspaceEnv) build(ctx context.Context, extra []string) (int, error) {
	if err := e.ensureUp(ctx); err != nil {
		return 1, err
	}
	p := dockerargs.BuildArgs(e.cfg, e.facts, extra, e.execOpts())
	return e.mgr.Docker.Attach(ctx, p.Args...)
}

func (e *workspaceEnv) exec(ctx context.Context, argv []string) (int, error) {
	if err := e.ensureUp(ctx); err != nil {
		return 1, err
	}
	p := dockerargs.ExecArgs(e.cfg, e.facts, argv, e.execOpts())
	return e.mgr.Docker.Attach(ctx, p.Args...)
}

func (e *workspaceEnv) execOpts() dockerargs.ExecOpts {
	tty := e.app.IsTTY() && !e.in.Globals.NoTTY
	return dockerargs.ExecOpts{TTY: tty, Interactive: true, Root: e.in.Globals.Root}
}

func (e *workspaceEnv) ensureUp(ctx context.Context) error {
	p, err := e.mgr.Ensure(ctx, e.in.Globals.Recreate)
	if err != nil {
		return err
	}
	for _, w := range p.Warnings {
		fmt.Fprintln(e.app.Stderr, "warning: "+w)
	}
	return nil
}
