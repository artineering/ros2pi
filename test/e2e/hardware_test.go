//go:build hardware

// Hardware tests need a real Raspberry Pi, not just Docker. They are tagged
// separately from `integration` so CI -- which has neither a Pi nor an I2C
// sensor -- can run everything else without pretending to cover this.
//
//	go test -tags hardware ./test/e2e/ -v
//
// These are the tests CI can never run, which makes them the ones most worth
// writing down: they are the only check that the tool reads real silicon
// correctly rather than merely reasoning correctly about fixtures.
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// facts is the subset of `ros2pi check --dump-facts` these tests assert on.
type facts struct {
	Model struct {
		Raw    string `json:"raw"`
		Family string `json:"family"`
	} `json:"model"`
	GPIOChips []struct {
		Name   string `json:"name"`
		Label  string `json:"label"`
		Dev    string `json:"dev"`
		Header bool   `json:"header"`
		Source string `json:"source"`
	} `json:"gpio_chips"`
	Groups []struct {
		Name           string `json:"name"`
		GID            int    `json:"gid"`
		Exists         bool   `json:"exists"`
		NameResolvable bool   `json:"name_resolvable"`
	} `json:"groups"`
	I2CHeader struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		Alias  string `json:"alias"`
	} `json:"i2c_header"`
}

func requirePi(t *testing.T) facts {
	t.Helper()
	b, err := os.ReadFile("/proc/device-tree/model")
	if err != nil || !strings.Contains(string(b), "Raspberry Pi") {
		t.Skip("not a Raspberry Pi")
	}
	out, err := exec.Command(binary, "check", "--dump-facts").Output()
	if err != nil {
		t.Fatalf("dump-facts: %v", err)
	}
	var f facts
	if err := json.Unmarshal(out, &f); err != nil {
		t.Fatalf("parse facts: %v", err)
	}
	return f
}

// The GPIO chip must be found by its LABEL, never its number.
//
// Kernel 6.6.47 moved the Pi 5's header from gpiochip4 back to gpiochip0 and
// broke shipping projects. Raspberry Pi OS also ships /dev/gpiochip4 as a
// symlink to gpiochip0 on a Pi 4, which a naive enumerator reports as a third
// chip that does not exist.
func TestGPIOChipIdentifiedByLabel(t *testing.T) {
	f := requirePi(t)

	var header int
	for _, c := range f.GPIOChips {
		if c.Header {
			header++
			if !strings.HasPrefix(c.Label, "pinctrl-") {
				t.Errorf("header chip %s has label %q, expected a pinctrl-* label",
					c.Name, c.Label)
			}
			if c.Source != "ioctl" {
				t.Errorf("chip label came from %q; the chardev ioctl is the "+
					"supported source, sysfs GPIO is deprecated", c.Source)
			}
		}
	}
	if header != 1 {
		t.Errorf("found %d header chips, want exactly 1: %+v", header, f.GPIOChips)
	}
}

// Cross-check our answer against gpiodetect, the reference implementation.
// Agreement with libgpiod is the strongest available evidence that the probe is
// right, since both go through the same kernel ABI.
func TestGPIOAgreesWithGpiodetect(t *testing.T) {
	f := requirePi(t)

	out, err := exec.Command("gpiodetect").Output()
	if err != nil {
		t.Skip("gpiodetect not installed (apt install gpiod)")
	}
	// gpiodetect prints: gpiochip0 [pinctrl-bcm2711] (58 lines)
	var want int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "gpiochip") {
			want++
		}
	}
	if len(f.GPIOChips) != want {
		t.Errorf("ros2pi found %d chips, gpiodetect found %d.\n"+
			"A mismatch usually means a compat symlink was counted as a real chip.\n"+
			"ros2pi: %+v\ngpiodetect:\n%s", len(f.GPIOChips), want, f.GPIOChips, out)
	}
}

// The Pi's gpio/i2c/spi groups are allocated dynamically and do not exist inside
// the ROS image, so they can only be granted numerically. Passing the NAME
// grants nothing, which is the single most common cause of "works with sudo,
// fails without" and of cargo-culted --privileged.
func TestPiGroupsAreNotNameResolvable(t *testing.T) {
	f := requirePi(t)

	for _, g := range f.Groups {
		switch g.Name {
		case "gpio", "i2c", "spi":
			if g.Exists && g.NameResolvable {
				t.Errorf("%s (gid %d) claims to resolve by name inside the ROS "+
					"image; it does not exist there, so --group-add %s would "+
					"grant nothing", g.Name, g.GID, g.Name)
			}
		case "dialout", "video":
			// These ARE static in Debian, so the name is safe -- but only if the
			// host actually uses the standard gid.
			if g.Exists && !g.NameResolvable {
				t.Logf("note: %s is gid %d, not the Debian standard; ros2pi will "+
					"pass it numerically (correct, just unusual)", g.Name, g.GID)
			}
		}
	}
}

// The headline claim of the whole project: a container can reach the GPIO
// hardware as an ordinary user, with no --privileged.
//
// This is what everyone else gets wrong. It is worth testing against the real
// device because the failure mode is EPERM at open() time -- the container
// starts fine, the node runs, and only the hardware call fails.
func TestContainerOpensGPIOChipAsNonRoot(t *testing.T) {
	f := requirePi(t)
	requireDocker(t)

	var chip string
	for _, c := range f.GPIOChips {
		if c.Header {
			chip = c.Dev
		}
	}
	if chip == "" {
		t.Skip("no header GPIO chip identified on this Pi")
	}

	ws := workspace(t)
	cfg := ws + "/ros2pi.toml"
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg,
		[]byte(strings.Replace(string(body), "gpio = false", "gpio = true", 1)),
		0o644); err != nil {
		t.Fatal(err)
	}

	// GPIO_GET_CHIPINFO_IOCTL: the same call ros2pi makes on the host. If the
	// group mapping is wrong this fails with EPERM, which is exactly the
	// failure users hit and cannot diagnose.
	script := `
import fcntl, os, struct, sys
uid = os.getuid()
if uid == 0:
    print("FAIL: running as root defeats the point of the test"); sys.exit(1)
f = open("` + chip + `", "rb")
buf = bytearray(68)
fcntl.ioctl(f, 0x8044B401, buf, True)
label = buf[32:64].split(b"\x00")[0].decode()
lines = struct.unpack("I", buf[64:68])[0]
print(f"OK uid={uid} label={label} lines={lines}")
`
	out := run(t, ws, "shell", "-c", "python3 -c '"+strings.ReplaceAll(script, "'", "'\\''")+"'")

	if !strings.Contains(out, "OK uid=") {
		t.Fatalf("a non-root container process could not open %s.\n"+
			"This is the failure ros2pi exists to prevent -- the group mapping "+
			"is wrong, or --privileged is being relied on.\n%s", chip, out)
	}
	if !strings.Contains(out, "pinctrl-") {
		t.Errorf("opened the chip but read no pinctrl label:\n%s", out)
	}
	t.Logf("non-root GPIO access confirmed: %s", strings.TrimSpace(out))
}

// I2C is the clearest case of a diagnosis nothing else makes: a Pi with I2C
// disabled still exposes /dev/i2c-0/10/20/21/22 for HDMI and camera buses, so
// globbing /dev/i2c-* concludes "i2c works" on a Pi where it demonstrably does
// not.
func TestI2CHeaderResolvedViaDeviceTreeAlias(t *testing.T) {
	f := requirePi(t)

	if f.I2CHeader.Source == "assumed" {
		t.Skip("no device-tree alias on this Pi; ros2pi warned and assumed, which is the intended fallback")
	}
	if f.I2CHeader.Source != "alias" {
		t.Errorf("i2c header source = %q, want it resolved via the alias", f.I2CHeader.Source)
	}
	if f.I2CHeader.Alias == "" {
		t.Error("resolved via alias but recorded no controller node")
	}

	// Enabled or not, the answer must be a CONCLUSION, not a guess: either a
	// real bus backed by the alias, or nothing because the controller is off.
	if f.I2CHeader.Path == "" {
		t.Logf("i2c is not enabled on this Pi (controller %s has no live bus) "+
			"-- ros2pi will refuse hardware.i2c with instructions, which is correct",
			f.I2CHeader.Alias)
	}
}
