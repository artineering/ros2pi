package dockerargs

import (
	"fmt"
	"sort"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

// Device cgroup majors. --device binds a node at CREATE time, so a USB device
// plugged in later is invisible -- and merely bind-mounting /dev is not enough
// either, because the device cgroup still denies open() with EPERM. Only a
// pre-authorised major range covers nodes that do not exist yet.
const (
	majorTTYUSB = 188 // /dev/ttyUSB* — FTDI/CP210x adapters, most LIDARs
	majorTTYACM = 166 // /dev/ttyACM* — CDC-ACM, most Arduinos
)

// addHardware translates the config's hardware requests into device and group
// flags, or explains precisely why it cannot.
//
// Nothing here is best-effort: if the workspace asks for i2c and the host has
// it disabled, that is an error with a fix, not a silently missing flag. A
// missing flag would surface later as an EPERM inside a user's node, which is
// the failure this tool exists to prevent.
func addHardware(b *builder, cfg config.Config, f hostfacts.HostFacts) error {
	if cfg.Hardware.Privileged {
		b.add("privileged: requested in config", "cfg.Hardware.Privileged", "--privileged")
		b.warn("hardware.privileged = true grants the container full access to this Pi. " +
			"It is almost never needed: ros2pi maps devices and groups individually. " +
			"If you needed it, that is a bug worth reporting.")
		return nil
	}

	// groups accumulates GIDs to grant. A set keyed by the flag value, resolved
	// to a sorted slice at the end -- argv must not depend on map order.
	groups := map[string]string{} // arg -> reason

	if cfg.Hardware.GPIO {
		if err := addGPIO(b, f, groups); err != nil {
			return err
		}
	}
	if cfg.Hardware.I2C {
		if err := addI2C(b, f, groups); err != nil {
			return err
		}
	}
	if cfg.Hardware.SPI {
		if err := addSPI(b, f, groups); err != nil {
			return err
		}
	}
	if cfg.Hardware.UART {
		if err := addUART(b, f, groups); err != nil {
			return err
		}
	}
	if cfg.Hardware.USBSerial {
		addUSBSerial(b, f, groups)
	}

	for _, d := range cfg.Hardware.ExtraDevices {
		b.add("extra device from config", "cfg.Hardware.ExtraDevices", "--device", d)
	}
	for _, name := range cfg.Hardware.ExtraGroups {
		if err := wantGroup(f, groups, name, "cfg.Hardware.ExtraGroups"); err != nil {
			return err
		}
	}

	// Emit --group-add in a stable order.
	for _, arg := range sortedKeys(groups) {
		b.add(groups[arg], "facts.Groups", "--group-add", arg)
	}

	if cfg.Hardware.GPIO || cfg.Hardware.I2C || cfg.Hardware.SPI {
		if f.Docker.Rootless {
			b.warn("docker is running rootless, which has no device-cgroup control. " +
				"Device access will likely fail at open() even though the container starts.")
		}
		if f.Docker.CgroupVersion == "1" {
			b.warn("cgroup v1 detected; device rules behave differently and are untested here.")
		}
	}
	return nil
}

// wantGroup records that a host group's GID must be granted.
//
// The Pi's gpio/i2c/spi groups are allocated dynamically by raspberrypi-sys-mods
// and do not exist inside the ROS image, so the NAME cannot resolve there --
// the numeric GID must be passed. Group.GroupAddArg encodes that decision;
// this function only handles the group being absent entirely.
func wantGroup(f hostfacts.HostFacts, groups map[string]string, name, source string) error {
	g, ok := f.Group(name)
	if !ok || !g.Exists {
		return errs.New(errs.CodeNotInGroup, "host group "+name+" does not exist").
			WithDetail("requested via %s, but no group %q is defined on this host", source, name).
			WithDetail("without it the container cannot be granted access to the device")
	}
	arg, ok := g.GroupAddArg()
	if !ok {
		return errs.New(errs.CodeNotInGroup, "cannot map host group "+name+" into the container")
	}
	reason := fmt.Sprintf("grant host group %s (gid %d)", name, g.GID)
	if !g.NameResolvable {
		reason += "; numeric because this group does not exist in the ROS image"
	}
	groups[arg] = reason
	return nil
}

func addGPIO(b *builder, f hostfacts.HostFacts, groups map[string]string) error {
	chip, ok := f.HeaderChip()
	if !ok {
		e := errs.New(errs.CodeNoGPIOChip, "cannot identify this Pi's GPIO controller").
			WithDetail("no /dev/gpiochip* carried a recognised Raspberry Pi pinctrl label")
		for _, c := range f.GPIOChips {
			e.WithDetail("  found %s [%s]", c.Name, c.Label)
		}
		e.WithDetail("").
			WithDetail("Guessing a chip would produce a --device flag that fails at open()").
			WithDetail("with a message you could not act on, so ros2pi refuses instead.").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "please report this, attaching:", Cmd: "ros2pi check --dump-facts"},
			}})
		return e
	}

	// The chip is identified by LABEL. Its number is a kernel-assigned artefact:
	// 6.6.47 moved the Pi 5 header from gpiochip4 back to gpiochip0.
	b.add(fmt.Sprintf("GPIO header chip, identified by label %q", chip.Label),
		"facts.HeaderChip()", "--device", chip.Dev)

	// gpiomem is the mmap window used by lgpio/gpiozero. Pi 5 renamed it.
	for _, m := range f.GPIOMem {
		if m.Exists {
			b.add("GPIO mmap window", "facts.GPIOMem", "--device", m.Path)
		}
	}
	return wantGroup(f, groups, "gpio", "hardware.gpio")
}

func addI2C(b *builder, f hostfacts.HostFacts, groups map[string]string) error {
	bus, ok := f.I2CHeaderBus()
	if !ok {
		return i2cNotEnabled(f)
	}
	b.add("I2C bus on header pins 3/5, resolved via the device-tree alias",
		"facts.I2CHeaderBus()", "--device", bus.Path)
	return wantGroup(f, groups, "i2c", "hardware.i2c")
}

// i2cNotEnabled explains the difference between "not enabled" and "missing",
// which is exactly what a user cannot tell from `ls /dev/i2c-*`: a Pi with I2C
// off still shows i2c-0/10/20/21/22 for HDMI DDC.
func i2cNotEnabled(f hostfacts.HostFacts) error {
	e := errs.New(errs.CodeI2CNotEnabled, "i2c is not enabled on this Pi").
		WithDetail("ros2pi.toml requests hardware.i2c = true, but:")

	if f.I2CHeader.Alias != "" {
		e.WithDetail("  the header's I2C controller is %s (per the device-tree alias)",
			f.I2CHeader.Alias)
		e.WithDetail("  it has no live bus, so the kernel never brought it up")
	} else {
		e.WithDetail("  no I2C controller could be resolved for the 40-pin header")
	}
	if f.Boot.I2CArm != hostfacts.On {
		e.WithDetail("  dtparam=i2c_arm is %s in %s", f.Boot.I2CArm, f.Boot.ConfigPath)
	}

	var decoys []string
	for _, b := range f.I2CBuses {
		if b.Exists {
			decoys = append(decoys, b.Path)
		}
	}
	if len(decoys) > 0 {
		sort.Strings(decoys)
		e.WithDetail("")
		e.WithDetail("Note: %v exist, but they are HDMI/DDC and camera buses,", decoys)
		e.WithDetail("not the header. Their presence does not mean i2c works.")
	}

	e.WithDetail("")
	e.WithDetail("The container cannot fix this: it is a firmware setting read at boot.")
	return e.WithFix(&errs.Fix{
		NeedsRoot:   true,
		NeedsReboot: true,
		Steps: []errs.Step{
			{Text: "enable i2c:", Cmd: "sudo raspi-config nonint do_i2c 0"},
			{Text: "reboot:", Cmd: "sudo reboot"},
			{Text: "then:", Cmd: "ros2pi up"},
		},
	})
}

func addSPI(b *builder, f hostfacts.HostFacts, groups map[string]string) error {
	var present []hostfacts.DevNode
	for _, d := range f.SPIDevs {
		if d.Exists {
			present = append(present, d)
		}
	}
	if len(present) == 0 {
		return errs.New(errs.CodeSPINotEnabled, "spi is not enabled on this Pi").
			WithDetail("ros2pi.toml requests hardware.spi = true, but no /dev/spidev* exists").
			WithDetail("dtparam=spi is %s in %s", f.Boot.SPI, f.Boot.ConfigPath).
			WithDetail("").
			WithDetail("The container cannot fix this: it is a firmware setting read at boot.").
			WithFix(&errs.Fix{
				NeedsRoot:   true,
				NeedsReboot: true,
				Steps: []errs.Step{
					{Text: "enable spi:", Cmd: "sudo raspi-config nonint do_spi 0"},
					{Text: "reboot:", Cmd: "sudo reboot"},
				},
			})
	}
	sort.Slice(present, func(i, j int) bool { return present[i].Path < present[j].Path })
	for _, d := range present {
		b.add("SPI device", "facts.SPIDevs", "--device", d.Path)
	}
	return wantGroup(f, groups, "spi", "hardware.spi")
}

func addUART(b *builder, f hostfacts.HostFacts, groups map[string]string) error {
	// /dev/serial0 is a SYMLINK. docker --device resolves it at create time and
	// exposes an inconsistently-named node inside, so pass the real one.
	var node string
	var via string
	for _, s := range f.Serial {
		if s.Path == "/dev/serial0" && s.Exists {
			node, via = s.Path, s.Resolved
			if s.Resolved != "" {
				node = s.Resolved
			}
			break
		}
	}
	if node == "" {
		return errs.New(errs.CodeConfigInvalid, "no serial port found on this Pi").
			WithDetail("ros2pi.toml requests hardware.uart = true, but /dev/serial0 does not exist").
			WithFix(&errs.Fix{
				NeedsRoot:   true,
				NeedsReboot: true,
				Steps: []errs.Step{
					{Text: "enable the serial port (and disable the login shell on it):",
						Cmd: "sudo raspi-config nonint do_serial_hw 0"},
					{Cmd: "sudo reboot"},
				},
			})
	}
	reason := "serial port"
	if via != "" {
		reason = fmt.Sprintf("serial port (/dev/serial0 -> %s, resolved because --device mishandles symlinks)", via)
	}
	b.add(reason, "facts.Serial", "--device", node)
	return wantGroup(f, groups, "dialout", "hardware.uart")
}

// addUSBSerial covers hotplug, which --device fundamentally cannot: it binds at
// create time, and the device cgroup denies open() on nodes that appear later
// even if /dev is bind-mounted. The two halves are both required.
func addUSBSerial(b *builder, f hostfacts.HostFacts, groups map[string]string) {
	b.add("bind /dev so hotplugged nodes appear (devtmpfs is live)",
		"", "-v", "/dev:/dev")
	b.add(fmt.Sprintf("pre-authorise ttyUSB* (major %d) for devices not yet plugged in", majorTTYUSB),
		"", "--device-cgroup-rule", fmt.Sprintf("c %d:* rmw", majorTTYUSB))
	b.add(fmt.Sprintf("pre-authorise ttyACM* (major %d) for devices not yet plugged in", majorTTYACM),
		"", "--device-cgroup-rule", fmt.Sprintf("c %d:* rmw", majorTTYACM))

	if g, ok := f.Group("dialout"); ok && g.Exists {
		if arg, ok := g.GroupAddArg(); ok {
			groups[arg] = fmt.Sprintf("grant host group dialout (gid %d) for USB serial", g.GID)
		}
	}
}
