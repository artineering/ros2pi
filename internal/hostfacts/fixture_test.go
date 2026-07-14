package hostfacts

import (
	"context"
	"os"
	"testing"
)

// TestRealPi4Fixture pins the interpretation of a fixture captured from real
// hardware (Raspberry Pi 4 Model B Rev 1.5, kernel 6.18.34, Debian 13).
//
// The unit tests above drive synthetic hosts, which only prove that the code is
// self-consistent. This one proves it still agrees with a machine that exists.
// Every bug report that includes `ros2pi check --dump-facts` should land here as
// another fixture -- that is how this stays honest without hardware in CI.
func TestRealPi4Fixture(t *testing.T) {
	f := loadFixture(t, "testdata/facts/pi4-k6.18-trixie.json")

	t.Run("family derives from chip label", func(t *testing.T) {
		if f.Model.Family != FamilyPi4 {
			t.Errorf("family = %q, want pi4", f.Model.Family)
		}
		c, ok := f.HeaderChip()
		if !ok || c.Label != "pinctrl-bcm2711" {
			t.Fatalf("header chip = %v, want label pinctrl-bcm2711", c)
		}
	})

	t.Run("no phantom chip from the gpiochip4 compat symlink", func(t *testing.T) {
		// Real /dev on this Pi contains gpiochip0, gpiochip1 and a
		// gpiochip4 -> gpiochip0 symlink. gpiodetect reports two.
		if len(f.GPIOChips) != 2 {
			t.Errorf("got %d chips, want 2", len(f.GPIOChips))
		}
	})

	t.Run("gpio group is dynamic and must be passed numerically", func(t *testing.T) {
		g, _ := f.Group("gpio")
		if !g.Exists || g.NameResolvable {
			t.Errorf("gpio = %+v, want exists and not name-resolvable", g)
		}
		// The exact GID is host-specific; the invariant is that it is neither
		// zero nor a Debian-static value, so it cannot resolve by name inside
		// the ROS image.
		if g.GID < 100 {
			t.Errorf("gpio gid = %d, implausible for a dynamic group", g.GID)
		}
	})

	t.Run("boot config resolves past the stub", func(t *testing.T) {
		if f.Boot.ConfigPath != "/boot/firmware/config.txt" {
			t.Errorf("config path = %q, want /boot/firmware/config.txt", f.Boot.ConfigPath)
		}
		if !f.Boot.StubSeen {
			t.Error("expected the /boot/config.txt stub to be noticed")
		}
	})

	t.Run("i2c header bus absent while decoy buses are present", func(t *testing.T) {
		// This Pi has i2c-0/10/20/21/22 (HDMI DDC etc.) but i2c_arm is unset,
		// so the header bus is absent. Globbing /dev/i2c-* would wrongly
		// conclude i2c works.
		if f.Boot.I2CArm != Unset {
			t.Fatalf("fixture drift: i2c_arm = %q, want unset", f.Boot.I2CArm)
		}
		if bus, ok := f.I2CHeaderBus(); ok {
			t.Errorf("header bus = %v, want absent (i2c_arm is unset)", bus)
		}
		var present int
		for _, b := range f.I2CBuses {
			if b.Exists {
				present++
			}
		}
		if present == 0 {
			t.Skip("fixture has no decoy buses; nothing to distinguish")
		}
	})

	t.Run("serial symlink resolved to an absolute node", func(t *testing.T) {
		for _, n := range f.Serial {
			if n.Path == "/dev/serial0" && n.Resolved != "" {
				if n.Resolved[0] != '/' {
					t.Errorf("serial0 resolved = %q, want an absolute path", n.Resolved)
				}
				return
			}
		}
	})

	t.Run("arch views agree", func(t *testing.T) {
		if f.Arch.Machine != "aarch64" || f.Arch.Dpkg != "arm64" {
			t.Errorf("arch = %+v, want aarch64/arm64", f.Arch)
		}
	})
}

func loadFixture(t *testing.T, path string) HostFacts {
	t.Helper()
	p := FileProber{FS: os.DirFS("."), Path: path}
	f, err := p.Probe(context.Background())
	if err != nil {
		t.Fatalf("load fixture %s: %v", path, err)
	}
	return f
}
