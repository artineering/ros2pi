package check

import (
	"fmt"
	"strings"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

func hostChecks() []Check {
	return []Check{
		{
			ID: "host.model", Section: "Host", Title: "model",
			Run: func(f hostfacts.HostFacts, _ *config.Config) Result {
				if f.Model.Raw == "" {
					return warn("unknown", "could not read /proc/device-tree/model")
				}
				return ok(f.Model.Raw)
			},
		},
		{
			ID: "host.family", Section: "Host", Title: "family",
			Run: func(f hostfacts.HostFacts, _ *config.Config) Result {
				if f.Model.Family == hostfacts.FamilyUnknown {
					return fail("unknown",
						"no GPIO chip carried a label this build recognises",
						"ros2pi will not guess at a device layout it does not know").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "please report this, attaching:", Cmd: "ros2pi check --dump-facts"},
						}})
				}
				r := ok(string(f.Model.Family))
				if c, found := f.HeaderChip(); found {
					r.Value = fmt.Sprintf("%s (from the GPIO label %q, not the model string)",
						f.Model.Family, c.Label)
				}
				if f.Model.Family == hostfacts.FamilyPi5 {
					r.Status = Warn
					r.Detail = append(r.Detail,
						"Pi 5 support is written from documentation and has never run on a real Pi 5.",
						"If something is wrong here, a --dump-facts in an issue would fix it for everyone.")
				}
				return r
			},
		},
		{
			ID: "host.arch", Section: "Host", Title: "arch",
			Run: func(f hostfacts.HostFacts, _ *config.Config) Result {
				views := [][2]string{
					{"kernel", f.Arch.Machine},
					{"userland", f.Arch.Dpkg},
					{"ros2pi", f.Arch.Go},
				}
				if f.Docker.ServerArch != "" {
					views = append(views, [2]string{"docker", f.Docker.ServerArch})
				}

				var bad []string
				for _, v := range views {
					switch norm(v[1]) {
					case "arm64", "amd64", "":
					default:
						bad = append(bad, fmt.Sprintf("%s=%s", v[0], v[1]))
					}
				}
				if len(bad) > 0 {
					// The classic Pi trap: a 64-bit kernel with a 32-bit
					// userland. uname says aarch64 and everything looks fine
					// until docker cannot find an image that runs.
					return fail(strings.Join(bad, " "),
						"ROS 2 images are published for arm64 and amd64 only -- there is no",
						"armv7 build -- and Docker Engine v29 dropped armhf packages for",
						"Raspberry Pi OS. This is closed upstream, not a ros2pi limitation.").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "reflash with 64-bit Raspberry Pi OS (Pi 3 and newer support it)"},
						}})
				}

				var parts []string
				for _, v := range views {
					parts = append(parts, fmt.Sprintf("%s=%s", v[0], v[1]))
				}
				return ok(strings.Join(parts, " "))
			},
		},
		{
			ID: "host.os", Section: "Host", Title: "os",
			Run: func(f hostfacts.HostFacts, _ *config.Config) Result {
				if f.OS.Pretty == "" {
					return warn("unknown", "could not read /etc/os-release")
				}
				return ok(f.OS.Pretty)
			},
		},
		{
			ID: "host.kernel", Section: "Host", Title: "kernel",
			Run: func(f hostfacts.HostFacts, _ *config.Config) Result {
				return ok(f.Kernel.Release)
			},
		},
		{
			ID: "host.user", Section: "Host", Title: "user",
			Run: func(f hostfacts.HostFacts, _ *config.Config) Result {
				id := f.Identity
				if id.UID == 0 {
					return fail("root",
						"ros2pi run as root would fill your workspace with files you",
						"cannot edit without sudo -- the exact problem it exists to prevent").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "run ros2pi as your normal user, without sudo"},
						}})
				}
				r := ok(fmt.Sprintf("%s (uid %d, gid %d)", id.Username, id.UID, id.GID))
				if id.UnderSudo {
					r.Status = Note
					r.Detail = append(r.Detail,
						"running under sudo; ros2pi resolved your real user from SUDO_UID",
						"so the container will not write root-owned files")
				}
				return r
			},
		},
	}
}

func norm(a string) string {
	switch a {
	case "aarch64", "arm64", "arm64/v8", "arm64v8":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	}
	return a
}
