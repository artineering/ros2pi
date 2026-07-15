package check

import (
	"fmt"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
	"github.com/artineering/ros2pi/internal/imagefacts"
)

func dockerChecks() []Check {
	return []Check{
		{
			ID: "docker.installed", Section: "Docker", Title: "installed",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				if f.Docker.Problem == hostfacts.DockerAbsent {
					return fail("not found",
						"ros2pi runs ROS 2 in a container, so docker is required").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "install it:", Cmd: "curl -fsSL https://get.docker.com | sh"},
							{Text: "then add yourself to the docker group:",
								Cmd: "sudo usermod -aG docker $USER && newgrp docker"},
						}})
				}
				v := f.Docker.ClientVersion
				if v == "" {
					v = "present"
				}
				return ok(fmt.Sprintf("%s (%s)", v, f.Docker.Binary))
			},
		},
		{
			ID: "docker.daemon", Section: "Docker", Title: "daemon",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				switch f.Docker.Problem {
				case hostfacts.DockerAbsent:
					return Result{Status: Skip}

				case hostfacts.DockerDaemonUnreachable:
					return fail("not running").
						withFix(&errs.Fix{NeedsRoot: true, Steps: []errs.Step{
							{Cmd: "sudo systemctl start docker"},
							{Text: "and to start it at boot:", Cmd: "sudo systemctl enable docker"},
						}})

				case hostfacts.DockerUnknownFailure:
					return fail("unusable", f.Docker.Detail).
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "please report this, attaching:", Cmd: "ros2pi check --dump-facts"},
						}})

				case hostfacts.DockerPermissionDenied, hostfacts.DockerPermissionStaleSession:
					// docker.group reports this; saying it twice would just be noise.
					return note("cannot reach it (see the docker group check below)")
				}
				return ok(fmt.Sprintf("reachable (server %s, %s)",
					f.Docker.ServerVersion, f.Docker.ServerArch))
			},
		},
		{
			// The check this whole tool is proudest of. Two states produce
			// IDENTICAL output from docker and need opposite fixes.
			ID: "docker.group", Section: "Docker", Title: "group",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				if f.Docker.Problem == hostfacts.DockerAbsent {
					return Result{Status: Skip}
				}
				g, found := f.Group("docker")

				switch f.Docker.Problem {
				case hostfacts.DockerPermissionStaleSession:
					return fail("added, but not in this session",
						fmt.Sprintf("/etc/group lists %s in `docker` (gid %d),",
							f.Identity.Username, g.GID),
						"but this shell's credentials predate that change.",
						"",
						"Running usermod again will not help. Nor will sudo:",
						"the group is already yours, this session just started before it.").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "start a shell that has the group:", Cmd: "newgrp docker"},
							{Text: "or log out and back in, which fixes it everywhere"},
						}})

				case hostfacts.DockerPermissionDenied:
					return fail("not a member",
						"reaching the docker socket requires membership of `docker`").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Cmd: "sudo usermod -aG docker $USER"},
							{Text: "then start a new session (no sudo needed):", Cmd: "newgrp docker"},
						}})
				}

				if !found || !g.Exists {
					return note("no docker group on this host")
				}
				return ok(fmt.Sprintf("%s is in docker (gid %d)", f.Identity.Username, g.GID))
			},
		},
		{
			ID: "docker.cgroup", Section: "Docker", Title: "cgroup",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				if !f.Docker.Usable() {
					return Result{Status: Skip}
				}
				if f.Docker.Rootless {
					// Rootless has no device-cgroup control at all, and the
					// failure appears at open() inside the user's node -- long
					// after the container started "fine".
					return warn("rootless",
						"rootless docker cannot grant device access.",
						"Containers will start, and hardware calls will fail with",
						"permission errors that look like a bug in your code.").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "use rootful docker if you need GPIO, I2C, SPI or serial"},
						}})
				}
				v := f.Docker.CgroupVersion
				if v == "1" {
					return warn("v1", "device rules behave differently on cgroup v1 and are untested here")
				}
				if v == "" {
					return note("unknown")
				}
				return ok("v" + v + ", rootful")
			},
		},
	}
}
