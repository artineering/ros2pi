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

func workspaceChecks() []Check {
	return []Check{
		{
			ID: "ws.root", Section: "Workspace", Title: "root",
			Run: func(_ hostfacts.HostFacts, cfg *config.Config, _ *imagefacts.Facts) Result {
				if cfg == nil {
					// Not an error: `ros2pi check` is meant to run anywhere,
					// especially before there is a workspace to check.
					return note("not inside a ros2pi workspace (run `ros2pi init` to make one)")
				}
				return ok(cfg.Root)
			},
		},
		{
			ID: "ws.ros", Section: "Workspace", Title: "ros",
			Run: func(_ hostfacts.HostFacts, cfg *config.Config, _ *imagefacts.Facts) Result {
				if cfg == nil {
					return Result{Status: Skip}
				}
				return ok(fmt.Sprintf("%s from %s", cfg.ROS.Distro, cfg.ROS.Image))
			},
		},
		{
			ID: "ws.domain", Section: "Workspace", Title: "domain",
			Run: func(_ hostfacts.HostFacts, cfg *config.Config, _ *imagefacts.Facts) Result {
				if cfg == nil {
					return Result{Status: Skip}
				}
				r := ok(fmt.Sprintf("ROS_DOMAIN_ID=%d", cfg.ROS.DomainID))
				if cfg.ROS.Network == "host" {
					// --network host puts every workspace on the same wire, so
					// two workspaces on one Pi see each other's nodes unless the
					// domain differs. Surprising, and hard to diagnose from the
					// symptom (ghost topics).
					r.Detail = append(r.Detail,
						"with --network host, another workspace on this Pi using the same",
						"domain id will see this one's nodes; change domain_id to isolate them")
				}
				return r
			},
		},
	}
}

func hardwareChecks() []Check {
	return []Check{
		{
			ID: "hw.gpiochip", Section: "Hardware", Title: "gpio chip",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				c, found := f.HeaderChip()
				if !found {
					var seen []string
					for _, x := range f.GPIOChips {
						seen = append(seen, fmt.Sprintf("%s[%s]", x.Name, x.Label))
					}
					return fail("not identified",
						"found: "+strings.Join(seen, " "),
						"none carried a label this build recognises, so ros2pi will not",
						"guess -- a wrong guess fails later with an error you cannot act on").
						withFix(&errs.Fix{Steps: []errs.Step{
							{Text: "please report this, attaching:", Cmd: "ros2pi check --dump-facts"},
						}})
				}
				return ok(fmt.Sprintf("%s [%s] %d lines, via %s",
					c.Name, c.Label, c.Lines, c.Source))
			},
		},
		{
			ID: "hw.gpiomem", Section: "Hardware", Title: "gpio mem",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				if p := joinPaths(f.GPIOMem); p != "" {
					return ok(p)
				}
				if f.Model.Family == hostfacts.FamilyUnknown {
					return note("not probed: the Pi family is unknown")
				}
				return warn("absent", "gpiozero and lgpio use this; some libraries will not work")
			},
		},
		{
			// The check that no other tool makes, and the one this project was
			// worth building for.
			ID: "hw.i2c", Section: "Hardware", Title: "i2c",
			Run: func(f hostfacts.HostFacts, cfg *config.Config, _ *imagefacts.Facts) Result {
				if bus, live := f.I2CHeaderBus(); live {
					return ok(fmt.Sprintf("%s (header pins 3/5)", bus.Path))
				}

				r := fail("not enabled")
				if f.I2CHeader.Alias != "" {
					r = r.withDetail("the header's controller is %s, per the device-tree alias",
						f.I2CHeader.Alias).
						withDetail("it has no live bus, so the kernel never brought it up")
				}
				if f.Boot.I2CArm != hostfacts.On {
					r = r.withDetail("dtparam=i2c_arm is %s in %s", f.Boot.I2CArm, f.Boot.ConfigPath)
				}

				// The decoys are the whole reason this check is not trivial.
				var decoys []string
				for _, b := range f.I2CBuses {
					if b.Exists {
						decoys = append(decoys, b.Path)
					}
				}
				if len(decoys) > 0 {
					sort.Strings(decoys)
					r = r.withDetail("").
						withDetail("Note: %s exist, but they are HDMI/DDC and camera buses,",
							strings.Join(decoys, ", ")).
						withDetail("not the header. `ls /dev/i2c-*` looking healthy means nothing.")
				}

				// Only a failure if the workspace actually wants i2c.
				if cfg != nil && !cfg.Hardware.I2C {
					r.Status = Note
					r.Value = "not enabled (and not requested)"
					return r
				}
				return r.withFix(&errs.Fix{
					NeedsRoot: true, NeedsReboot: true,
					Steps: []errs.Step{
						{Text: "enable it:", Cmd: "sudo raspi-config nonint do_i2c 0"},
						{Cmd: "sudo reboot"},
					},
				})
			},
		},
		{
			ID: "hw.spi", Section: "Hardware", Title: "spi",
			Run: func(f hostfacts.HostFacts, cfg *config.Config, _ *imagefacts.Facts) Result {
				if p := joinPaths(f.SPIDevs); p != "" {
					return ok(p)
				}
				r := fail("not enabled").
					withDetail("dtparam=spi is %s in %s", f.Boot.SPI, f.Boot.ConfigPath)
				if cfg != nil && !cfg.Hardware.SPI {
					r.Status = Note
					r.Value = "not enabled (and not requested)"
					return r
				}
				return r.withFix(&errs.Fix{
					NeedsRoot: true, NeedsReboot: true,
					Steps: []errs.Step{
						{Text: "enable it:", Cmd: "sudo raspi-config nonint do_spi 0"},
						{Cmd: "sudo reboot"},
					},
				})
			},
		},
		{
			ID: "hw.serial", Section: "Hardware", Title: "serial",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				for _, s := range f.Serial {
					if s.Path == "/dev/serial0" && s.Exists {
						v := s.Path
						if s.Resolved != "" {
							// The resolution matters: --device on a symlink
							// exposes an inconsistently-named node inside.
							v = fmt.Sprintf("%s -> %s", s.Path, s.Resolved)
						}
						return ok(v)
					}
				}
				return note("no /dev/serial0", "enable it with `sudo raspi-config nonint do_serial_hw 0` if you need it")
			},
		},
		{
			ID: "hw.usbserial", Section: "Hardware", Title: "usb serial",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				if p := joinPaths(f.USBSerial); p != "" {
					return ok(p)
				}
				return note("none connected", "LIDARs and Arduinos appear here when plugged in")
			},
		},
		{
			ID: "hw.groups", Section: "Hardware", Title: "groups",
			Run: func(f hostfacts.HostFacts, _ *config.Config, _ *imagefacts.Facts) Result {
				var lines []string
				var missing []string

				for _, name := range []string{"gpio", "i2c", "spi", "dialout"} {
					g, found := f.Group(name)
					if !found || !g.Exists {
						continue
					}
					how := fmt.Sprintf("%d (numeric: not in the ROS image)", g.GID)
					if g.NameResolvable {
						how = fmt.Sprintf("%s (by name: %d matches the image)", name, g.GID)
					}
					lines = append(lines, fmt.Sprintf("%-8s -> --group-add %s", name, how))

					if !g.UserIsMember {
						missing = append(missing, name)
					}
				}
				if len(lines) == 0 {
					return note("none of the usual hardware groups exist")
				}

				r := ok("mapped")
				r.Detail = lines
				if len(missing) > 0 {
					r.Status = Warn
					r.Value = fmt.Sprintf("%s is not in: %s",
						f.Identity.Username, strings.Join(missing, ", "))
					r.Detail = append(r.Detail, "",
						"ros2pi will still grant these to the container, so your nodes work.",
						"You just cannot use those devices from your own shell.")
					r.Fix = &errs.Fix{Steps: []errs.Step{
						{Cmd: "sudo usermod -aG " + strings.Join(missing, ",") + " $USER"},
						{Text: "then log out and back in"},
					}}
				}
				return r
			},
		},
	}
}
