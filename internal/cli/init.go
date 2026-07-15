package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/artineering/ros2pi/internal/check"
	"github.com/artineering/ros2pi/internal/distro"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
	"github.com/artineering/ros2pi/internal/imagefacts"
)

// cmdInit scaffolds a workspace.
func (a App) cmdInit(in Invocation) error {
	flagDistro, rest, err := takeDistroFlag(in.OwnArgs)
	if err != nil {
		return err
	}

	dir := in.Globals.Workspace
	if dir == "" {
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			dir = rest[0]
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			dir = wd
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	path := filepath.Join(abs, config.FileName)
	if _, err := os.Stat(path); err == nil {
		return errs.New(errs.CodeWorkspaceInit, "this is already a ros2pi workspace").
			WithDetail("%s exists", path)
	}

	if err := os.MkdirAll(filepath.Join(abs, "src"), 0o755); err != nil {
		return err
	}

	chosen, err := a.pickDistro(flagDistro, a.Stdin, a.IsTTY())
	if err != nil {
		return err
	}

	cfg := config.Default()
	cfg.ROS.Distro = chosen
	cfg.ROS.Image = distro.Image(chosen)

	body, err := config.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(configHeader(), body...), 0o644); err != nil {
		return err
	}

	gi := filepath.Join(abs, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) {
		// build/install/log hold binaries linked against the CONTAINER's ROS.
		// They are host files but will not run on the host, and committing them
		// is never right.
		_ = os.WriteFile(gi, []byte("build/\ninstall/\nlog/\n"), 0o644)
	}

	fmt.Fprintf(a.Stdout, "\ncreated %s (ROS 2 %s, from %s)\n", path, chosen, cfg.ROS.Image)
	fmt.Fprintf(a.Stdout, "\nNext:\n")
	fmt.Fprintf(a.Stdout, "  ros2pi pkg create --build-type ament_python --node-name my_node \\\n")
	fmt.Fprintf(a.Stdout, "        --destination-directory src my_pkg\n")
	fmt.Fprintf(a.Stdout, "  ros2pi build\n")
	fmt.Fprintf(a.Stdout, "  ros2pi run my_pkg my_node\n")
	return nil
}

func configHeader() []byte {
	return []byte(`# ros2pi workspace configuration.
#
# Hardware entries are REQUESTS, not assertions: if you ask for i2c and this Pi
# has it disabled, ros2pi tells you how to enable it rather than starting a
# container where the device silently does not work.

`)
}

// cmdCheck reports on the host.
//
// It deliberately requires neither a workspace nor a working docker: the moment
// a user most needs this is the moment their setup is broken, and a diagnostic
// that refuses to run when things are wrong is not a diagnostic.
func (a App) cmdCheck(ctx context.Context, in Invocation) (int, error) {
	var dump, asJSON, strict bool
	var explain string

	for i := 0; i < len(in.OwnArgs); i++ {
		switch arg := in.OwnArgs[i]; arg {
		case "--dump-facts":
			dump = true
		case "--json":
			asJSON = true
		case "--strict":
			strict = true
		case "--explain":
			if i+1 >= len(in.OwnArgs) {
				return 2, fmt.Errorf("--explain needs a check id, e.g. --explain hw.i2c\n  ids: %s",
					strings.Join(check.IDs(), " "))
			}
			explain = in.OwnArgs[i+1]
			i++
		default:
			if v, ok := strings.CutPrefix(arg, "--explain="); ok {
				explain = v
				continue
			}
			return 2, fmt.Errorf("unknown flag for `ros2pi check`: %s", arg)
		}
	}

	f, err := a.probe(ctx)
	if err != nil {
		return 1, err
	}

	if dump {
		return 0, f.Dump(a.Stdout)
	}

	// A config is optional: nil simply means the workspace checks report that
	// there is no workspace, rather than the whole command refusing to run.
	cfg := a.tryConfig(in)

	if explain != "" {
		return a.explain(explain, f, cfg, a.tryImage(ctx, f, cfg))
	}

	// The image is probed only when there is a workspace to name one, and a
	// failure to ask is not fatal: `ros2pi check` must still report on a Pi
	// whose docker is broken, which is exactly when it is needed most.
	img := a.tryImage(ctx, f, cfg)

	rep := check.Run(f, cfg, img)

	if asJSON {
		if err := check.RenderJSON(a.Stdout, rep); err != nil {
			return 1, err
		}
	} else {
		check.Render(a.Stdout, rep, a.colour())
	}

	if rep.Failed > 0 || (strict && rep.Warned > 0) {
		return 1, nil
	}
	return 0, nil
}

// tryImage asks docker about the workspace's image, and shrugs if it cannot.
func (a App) tryImage(ctx context.Context, f hostfacts.HostFacts, cfg *config.Config) *imagefacts.Facts {
	if cfg == nil || !f.Docker.Usable() {
		return nil
	}
	img := imagefacts.Probe(ctx, hostRunner{hostfacts.NewOSHost()}, cfg.ROS.Image, f.Arch.Machine)
	return &img
}

// hostRunner adapts a HostIO to what imagefacts needs. The two packages agree
// on the shape of a command result but not on the type, so that neither has to
// import the other.
type hostRunner struct{ io hostfacts.HostIO }

func (h hostRunner) Exec(ctx context.Context, name string, args ...string) (imagefacts.Result, error) {
	r, err := h.io.Exec(ctx, name, args...)
	return imagefacts.Result{Stdout: r.Stdout, Stderr: r.Stderr, Code: r.Code}, err
}

// tryConfig loads the workspace config if there is one, and shrugs if not.
func (a App) tryConfig(in Invocation) *config.Config {
	dir := in.Globals.Workspace
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil
		}
		dir = wd
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil
	}
	return &cfg
}

// explain prints one check's verdict on its own, for when the report says
// something surprising and the user wants to know why.
func (a App) explain(id string, f hostfacts.HostFacts, cfg *config.Config, img *imagefacts.Facts) (int, error) {
	c, found := check.Find(id)
	if !found {
		return 2, fmt.Errorf("no check called %q\n  ids: %s", id, strings.Join(check.IDs(), " "))
	}
	r := c.Run(f, cfg, img)

	fmt.Fprintf(a.Stdout, "%s  (%s)\n\n", c.ID, c.Section)
	fmt.Fprintf(a.Stdout, "  %s  %s  %s\n", r.Status, c.Title, r.Value)
	for _, d := range r.Detail {
		fmt.Fprintf(a.Stdout, "        %s\n", d)
	}
	if r.Fix != nil {
		for _, s := range r.Fix.Steps {
			if s.Text != "" {
				fmt.Fprintf(a.Stdout, "        fix: %s\n", s.Text)
			}
			if s.Cmd != "" {
				fmt.Fprintf(a.Stdout, "             %s\n", s.Cmd)
			}
		}
	}
	return 0, nil
}

// colour is on only for a terminal: escape codes in a pipe or a pasted bug
// report are noise.
func (a App) colour() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return a.IsTTY()
}

func factsProber(osPath string) (hostfacts.Prober, error) {
	abs, err := filepath.Abs(osPath)
	if err != nil {
		return nil, fmt.Errorf("resolve ROS2PI_FACTS=%q: %w", osPath, err)
	}
	rel := strings.TrimPrefix(filepath.ToSlash(abs), "/")
	if rel == "" {
		return nil, fmt.Errorf("ROS2PI_FACTS=%q does not name a file", osPath)
	}
	return hostfacts.FileProber{FS: os.DirFS("/"), Path: rel}, nil
}

func (a App) usage() {
	fmt.Fprintf(a.Stderr, `ros2pi %s — run ROS 2 on a Raspberry Pi, without installing ROS on the Pi

usage:
  ros2pi [flags] <command> [args...]

ros2pi commands:
`, a.Version)
	for _, v := range OwnVerbs() {
		fmt.Fprintf(a.Stderr, "  %-8s %s\n", v, VerbHelp(v))
	}
	fmt.Fprintf(a.Stderr, `
anything else is passed to ros2 inside the container:
  ros2pi run my_pkg my_node
  ros2pi topic list
  ros2pi launch my_pkg my_launch.py

flags (these go BEFORE the command; everything after it goes to ros2):
  -C, --workspace DIR   workspace to act on (default: search upward from cwd)
  -v, --verbose         print each docker command
      --dry-run         print what would run, and stop
      --recreate        recreate the container even if it is running
      --root            run as root inside the container (rosdep, apt)
      --no-tty          never allocate a TTY
      --                pass everything after this to ros2 verbatim

`)
}

// takeDistroFlag pulls --distro out of init's arguments.
//
// It is parsed here rather than as a global because it only means anything to
// `init`: on any other command the distro comes from ros2pi.toml, and a flag
// that silently did nothing would be worse than no flag.
func takeDistroFlag(args []string) (value string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--distro":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--distro needs a value, e.g. --distro jazzy")
			}
			value = args[i+1]
			i++
		case strings.HasPrefix(a, "--distro="):
			value = strings.TrimPrefix(a, "--distro=")
		default:
			rest = append(rest, a)
		}
	}
	return value, rest, nil
}
