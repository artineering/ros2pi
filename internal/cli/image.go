package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/docker"
	"github.com/artineering/ros2pi/internal/dockerargs"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/image"
)

// image handles `ros2pi image <build|print>`.
func (e *workspaceEnv) image(ctx context.Context, args []string) (int, error) {
	sub := "build"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, rest = args[0], args[1:]
	}

	switch sub {
	case "build":
		return e.imageBuild(ctx, rest)
	case "print":
		fmt.Fprint(e.app.Stdout, image.Dockerfile(e.cfg))
		return 0, nil
	}
	return 2, fmt.Errorf("unknown: ros2pi image %s\n  try: ros2pi image build | ros2pi image print", sub)
}

// imageBuild bakes the workspace's declared dependencies into an image.
func (e *workspaceEnv) imageBuild(ctx context.Context, args []string) (int, error) {
	noCache := false
	for _, a := range args {
		if a == "--no-cache" {
			noCache = true
		}
	}

	manifests, err := image.FindManifests(e.cfg.Root)
	if err != nil {
		return 1, err
	}
	if len(manifests) == 0 {
		return 1, errs.New(errs.CodeNoWorkspace, "no packages found in src/").
			WithDetail("there is nothing to read dependencies from").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "create a package first:",
					Cmd: "ros2pi pkg create --build-type ament_python --node-name my_node my_pkg"},
			}})
	}

	pkgs := image.PackageNames(manifests)
	fmt.Fprintf(e.app.Stdout, "reading dependencies from %d package(s): %s\n",
		len(manifests), strings.Join(pkgs, ", "))

	// A temp context, holding only the manifests: see image.StageContext.
	dir, err := os.MkdirTemp("", "ros2pi-build-*")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(dir)

	if err := image.StageContext(e.cfg, manifests, dir); err != nil {
		return 1, err
	}

	tag := image.Name(e.cfg, dockerargs.ContainerName(e.cfg.Root))
	fmt.Fprintf(e.app.Stdout, "building %s\n", tag)

	// Attach rather than capture: an image build is slow and the user needs to
	// watch it, especially the rosdep step where their dependencies resolve.
	code, err := e.mgr.Docker.Attach(ctx, image.BuildArgs(e.cfg, tag, dir, noCache)...)
	if err != nil {
		return 1, err
	}
	if code != 0 {
		return code, errs.New(errs.CodeDockerUnknown, "the image build failed").
			WithDetail("the output above is from docker and rosdep").
			WithDetail("a common cause is a typo in a <depend> in one of your package.xml files").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "see the exact Dockerfile ros2pi used:", Cmd: "ros2pi image print"},
			}})
	}

	if e.in.Globals.DryRun {
		return 0, nil
	}
	return 0, e.adoptImage(tag)
}

// adoptImage points the workspace at the image just built.
//
// The config is rewritten rather than the image silently preferred: a container
// running a different image from the one ros2pi.toml names would be a genuinely
// baffling thing to debug. The change is announced, and it is a normal edit the
// user can undo.
func (e *workspaceEnv) adoptImage(tag string) error {
	if e.cfg.ROS.Image == tag {
		fmt.Fprintf(e.app.Stdout, "\n%s is ready. ros2pi.toml already points at it.\n", tag)
		fmt.Fprintf(e.app.Stdout, "Apply it with: ros2pi up --recreate\n")
		return nil
	}

	prev := e.cfg.ROS.Image
	cfg := e.cfg
	// Remember what we built FROM, so the next build does not stack this image
	// on top of itself.
	if cfg.Image.Base == "" {
		cfg.Image.Base = prev
	}
	cfg.ROS.Image = tag

	if err := config.Save(cfg); err != nil {
		return err
	}

	fmt.Fprintf(e.app.Stdout, "\n%s is ready.\n\n", tag)
	fmt.Fprintf(e.app.Stdout, "ros2pi.toml updated:\n")
	fmt.Fprintf(e.app.Stdout, "  ros.image  = %q   (was %q)\n", tag, prev)
	fmt.Fprintf(e.app.Stdout, "  image.base = %q   (what to build from next time)\n\n", cfg.Image.Base)
	fmt.Fprintf(e.app.Stdout, "Apply it with: ros2pi up --recreate\n")
	return nil
}

// dockerClientFor is a small helper so `image` can run before a container
// exists.
var _ = docker.Client{}
