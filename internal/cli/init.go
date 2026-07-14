package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

// cmdInit scaffolds a workspace.
func (a App) cmdInit(in Invocation) error {
	dir := in.Globals.Workspace
	if dir == "" {
		if len(in.OwnArgs) > 0 && !strings.HasPrefix(in.OwnArgs[0], "-") {
			dir = in.OwnArgs[0]
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

	cfg := config.Default()
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

	fmt.Fprintf(a.Stdout, "created %s\n", path)
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

// cmdCheck currently only implements --dump-facts; the human-readable report is
// M2. It deliberately does NOT require a workspace or a working docker: the
// whole point is to run when things are broken.
func (a App) cmdCheck(ctx context.Context, in Invocation) (int, error) {
	dump := false
	for _, arg := range in.OwnArgs {
		if arg == "--dump-facts" {
			dump = true
		}
	}
	if !dump {
		return 2, fmt.Errorf("`ros2pi check` reporting is not implemented yet (M2)\n" +
			"  for now: ros2pi check --dump-facts")
	}

	f, err := a.probe(ctx)
	if err != nil {
		return 1, err
	}
	if err := f.Dump(a.Stdout); err != nil {
		return 1, err
	}
	return 0, nil
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
