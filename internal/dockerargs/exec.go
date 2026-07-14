package dockerargs

import (
	"strconv"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

// ExecOpts describes how a command should be attached.
type ExecOpts struct {
	// TTY should be true only when stdin AND stdout are terminals. Allocating a
	// TTY when stdout is a pipe corrupts the stream with control characters and
	// breaks `ros2pi topic echo | head`.
	TTY bool

	// Root runs the command as uid 0 inside the container. rosdep and apt need
	// it; nothing else should.
	Root bool

	// Interactive keeps stdin open. Almost always true: even non-TTY commands
	// may read stdin from a pipe.
	Interactive bool
}

// ExecArgs builds `docker exec` for a command inside the workspace container.
//
// argv is forwarded VERBATIM. It is never parsed, rewritten, quoted or
// re-split: it goes to exec.Command as a []string, so there is no shell on the
// host side and no quoting to get wrong. The only shell involved is the
// constant embedded shim.
func ExecArgs(cfg config.Config, f hostfacts.HostFacts, argv []string, o ExecOpts) Plan {
	b := &builder{}
	name := ContainerName(cfg.Root)

	b.add("run a command in the existing workspace container", "", "exec")

	if o.Interactive {
		b.add("keep stdin open", "", "-i")
	}
	if o.TTY {
		// Only when both ends are terminals; see ExecOpts.TTY.
		b.add("both stdin and stdout are terminals", "", "-t")
	}
	if o.Root {
		b.add("explicitly requested root (rosdep/apt need it)", "", "--user", "0:0")
	}

	b.add("start in the workspace", "", "-w", WorkspaceDir)
	b.add("the workspace container", "", name)

	// The shim sources the ROS setup and then execs argv. It is a real file
	// bind-mounted in, not an inline string, so it is debuggable and so
	// `ros2pi shell` can use it as an --rcfile.
	b.add("entry shim: sources ROS, verifies the distro, then execs", "",
		"bash", ShimDir+"/entry.sh")
	b.args = append(b.args, argv...)

	return Plan{
		Args:      b.args,
		Container: name,
		Image:     cfg.ROS.Image,
		Decisions: b.decisions,
	}
}

// BuildArgs builds the colcon invocation for `ros2pi build`.
func BuildArgs(cfg config.Config, f hostfacts.HostFacts, extra []string, o ExecOpts) Plan {
	argv := []string{"colcon", "build"}
	argv = append(argv, cfg.Build.ColconArgs...)
	if cfg.Build.ParallelWorkers > 0 {
		argv = append(argv, "--parallel-workers", strconv.Itoa(cfg.Build.ParallelWorkers))
	}
	argv = append(argv, extra...)
	return ExecArgs(cfg, f, argv, o)
}

// StartArgs starts an existing, stopped container.
func StartArgs(cfg config.Config) Plan {
	name := ContainerName(cfg.Root)
	return Plan{
		Args:      []string{"start", name},
		Container: name,
		Decisions: []Decision{{Arg: "start", Reason: "restart the existing workspace container"}},
	}
}

// StopArgs stops a running container.
func StopArgs(cfg config.Config) Plan {
	name := ContainerName(cfg.Root)
	return Plan{
		Args:      []string{"stop", name},
		Container: name,
		Decisions: []Decision{{Arg: "stop", Reason: "stop the workspace container"}},
	}
}

// RemoveArgs removes a container, running or not.
func RemoveArgs(cfg config.Config) Plan {
	name := ContainerName(cfg.Root)
	return Plan{
		Args:      []string{"rm", "-f", name},
		Container: name,
		Decisions: []Decision{{Arg: "rm", Reason: "remove the workspace container"}},
	}
}

// InspectArgs asks docker about the workspace container. The output format is
// chosen so lifecycle can parse state and labels in one call.
//
// --type container is essential, not tidiness. `docker inspect NAME` searches
// containers AND images, and `ros2pi image build` produces an image named after
// the same workspace. Once the container is removed, a bare inspect resolves to
// that image instead, which has no .State, and the format template dies with
// "map has no entry for key State" -- an error with no relationship to what the
// user did.
func InspectArgs(cfg config.Config) Plan {
	name := ContainerName(cfg.Root)
	const format = `{{.State.Status}}|{{index .Config.Labels "` + LabelPlan + `"}}|{{index .Config.Labels "` + LabelImage + `"}}`
	return Plan{
		Args:      []string{"inspect", "--type", "container", "--format", format, name},
		Container: name,
		Decisions: []Decision{{Arg: "inspect", Reason: "read container state and labels"}},
	}
}
