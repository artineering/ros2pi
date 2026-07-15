package check

import (
	"fmt"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
	"github.com/artineering/ros2pi/internal/imagefacts"
)

func imageChecks() []Check {
	return []Check{
		{
			ID: "image.present", Section: "Image", Title: "image",
			Run: func(f hostfacts.HostFacts, cfg *config.Config, img *imagefacts.Facts) Result {
				if cfg == nil || img == nil {
					return Result{Status: Skip}
				}
				switch img.Problem {
				case imagefacts.Absent:
					// Not an error -- docker will fetch it. But saying nothing
					// means the next command sits silently for several minutes
					// on a Pi's SD card and looks hung.
					return warn(img.Ref+" is not pulled yet",
						"your next command will download it (roughly 1.3 GB for a ROS image),",
						"which takes a while on a Pi and looks like nothing is happening").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "pull it now, so it is not a surprise later:", Cmd: "ros2pi up"},
						}})

				case imagefacts.Unknown:
					return fail("cannot inspect "+img.Ref, img.Detail).
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "please report this, attaching:", Cmd: "ros2pi check --dump-facts"},
						}})
				}

				v := img.Ref
				if img.Size != "" {
					v += ", " + img.Size
				}
				r := ok(v)
				if img.Local() {
					r.Detail = append(r.Detail,
						"built locally (no registry digest), which is what `ros2pi image build` produces")
				} else if d := img.ShortDigest(); d != "" {
					r.Detail = append(r.Detail, "digest "+d)
				}
				return r
			},
		},
		{
			// The failure this catches is the whole reason the project is
			// arm64-only, and it is completely baffling when it happens: the
			// image pulls fine and then nothing runs.
			ID: "image.arch", Section: "Image", Title: "arch",
			Run: func(f hostfacts.HostFacts, cfg *config.Config, img *imagefacts.Facts) Result {
				if cfg == nil || img == nil || !img.Present {
					return Result{Status: Skip}
				}
				if img.Problem == imagefacts.WrongArch {
					return fail(fmt.Sprintf("%s/%s, but this Pi is %s",
						img.OS, img.Arch, norm(f.Arch.Machine)),
						"this image cannot run here. It was probably pulled on a different",
						"machine, or forced with --platform.").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "remove it and let ros2pi pull the right one:",
								Cmd: "docker rmi " + img.Ref},
							{Cmd: "ros2pi up"},
						}})
				}
				return ok(fmt.Sprintf("%s/%s", img.OS, img.Arch))
			},
		},
		{
			// A mismatch here is caught by the shim at run time too, but a
			// container that refuses to start is a worse way to learn it than a
			// line in a report.
			ID: "image.distro", Section: "Image", Title: "distro",
			Run: func(_ hostfacts.HostFacts, cfg *config.Config, img *imagefacts.Facts) Result {
				if cfg == nil || img == nil || !img.Present {
					return Result{Status: Skip}
				}
				if img.ROSDistro == "" {
					return warn("the image does not say which ROS it contains",
						"ros2pi expected "+cfg.ROS.Distro+"; without ROS_DISTRO in the image",
						"there is no way to confirm that before running it").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "if this is not a ROS image, point ros.image at one"},
						}})
				}
				if img.ROSDistro != cfg.ROS.Distro {
					return fail(fmt.Sprintf("image has %s, ros2pi.toml wants %s",
						img.ROSDistro, cfg.ROS.Distro),
						"These must match. Without ros2pi you would not find out:",
						"the image sources its own ROS first, so the wrong one would",
						"simply work, quietly, until something behaved oddly.").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: fmt.Sprintf("use the image's ROS: set distro = %q in ros2pi.toml",
								img.ROSDistro)},
							{Text: fmt.Sprintf("or use the distro you asked for: set image = %q",
								"ros:"+cfg.ROS.Distro)},
						}})
				}
				return ok(img.ROSDistro + " (matches ros2pi.toml)")
			},
		},
	}
}
