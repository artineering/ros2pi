package hostfacts

import (
	"context"
	"testing"
)

// withI2CTree adds a device-tree alias and the corresponding sysfs bus entries
// to a fake host, mirroring the layout observed on a real Pi 4.
func withI2CTree(p fakePi, aliasName, aliasTarget string, buses map[string]string) HostIO {
	if p.extraFiles == nil {
		p.extraFiles = map[string]string{}
	}
	if aliasName != "" {
		// Device-tree property strings are NUL-terminated.
		p.extraFiles["proc/device-tree/aliases/"+aliasName] = aliasTarget + "\x00"
	}
	links := map[string]string{}
	for bus, dtNode := range buses {
		p.extraFiles["sys/bus/i2c/devices/"+bus+"/of_node"] = ""
		p.extraFiles["dev/"+bus] = ""
		// of_node is a RELATIVE symlink on a real host, and one whose ../.. is
		// relative to the link's physical location, not its /sys/bus path.
		links["/sys/bus/i2c/devices/"+bus+"/of_node"] =
			"../../../../../firmware/devicetree/base" + dtNode
	}

	base := p.host()
	return override{HostIO: base, readlink: func(path string) (string, error) {
		if t, ok := links[path]; ok {
			return t, nil
		}
		return base.Readlink(path)
	}}
}

// The header bus number is resolved through the device-tree alias, never
// hardcoded -- the same principle that governs GPIO chip numbers.
func TestI2CHeader_ResolvedViaAliasNotHardcoded(t *testing.T) {
	// Deliberately place the header controller on i2c-7, NOT the conventional
	// i2c-1: a hardcoded implementation passes this test only by accident.
	env := withI2CTree(pi5(), "i2c_arm", "/soc/i2c@7e804000", map[string]string{
		"i2c-7":  "/soc/i2c@7e804000", // the header controller
		"i2c-22": "/soc/i2c@7e205000", // an HDMI/DDC decoy
	})
	env = override{HostIO: env, statDev: func(path string) (DevStat, error) {
		if path == "/dev/i2c-7" || path == "/dev/i2c-22" {
			return DevStat{Exists: true, IsChar: true, Major: 89, Mode: 0o660, OwnerGID: 994}, nil
		}
		return DevStat{Exists: false}, nil
	}}

	f, err := NewLinuxProber(env, "test").Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.I2CHeader.Source != I2CFromAlias {
		t.Errorf("source = %q, want %q", f.I2CHeader.Source, I2CFromAlias)
	}
	if f.I2CHeader.Path != "/dev/i2c-7" {
		t.Errorf("header bus = %q, want /dev/i2c-7 (resolved via alias, not assumed i2c-1)",
			f.I2CHeader.Path)
	}
	bus, ok := f.I2CHeaderBus()
	if !ok || bus.Path != "/dev/i2c-7" {
		t.Errorf("I2CHeaderBus() = %v/%v, want /dev/i2c-7", bus, ok)
	}
}

// i2c_arm is preferred, but not every model defines it; i2c1 is the fallback.
func TestI2CHeader_FallsBackToI2C1Alias(t *testing.T) {
	env := withI2CTree(pi5(), "i2c1", "/soc/i2c@7e804000", map[string]string{
		"i2c-1": "/soc/i2c@7e804000",
	})
	env = override{HostIO: env, statDev: func(path string) (DevStat, error) {
		if path == "/dev/i2c-1" {
			return DevStat{Exists: true, IsChar: true, Major: 89}, nil
		}
		return DevStat{Exists: false}, nil
	}}
	f, _ := NewLinuxProber(env, "test").Probe(context.Background())
	if f.I2CHeader.Path != "/dev/i2c-1" || f.I2CHeader.Source != I2CFromAlias {
		t.Errorf("header = %+v, want /dev/i2c-1 via alias", f.I2CHeader)
	}
}

// The alias identifying a controller that has no live bus is the NORMAL state
// when i2c is disabled. It is not an error: it is exactly the diagnosis
// "the header bus exists in the device tree but is not enabled", which is what
// distinguishes "not enabled" from "enabled on a different number".
func TestI2CHeader_AliasPresentButBusDisabled(t *testing.T) {
	env := withI2CTree(pi5(), "i2c_arm", "/soc/i2c@7e804000", map[string]string{
		"i2c-22": "/soc/i2c@7e205000", // only the decoy is live
	})
	f, _ := NewLinuxProber(env, "test").Probe(context.Background())

	if f.I2CHeader.Source != I2CFromAlias {
		t.Errorf("source = %q, want alias", f.I2CHeader.Source)
	}
	if f.I2CHeader.Path != "" {
		t.Errorf("path = %q, want empty: the controller has no live bus", f.I2CHeader.Path)
	}
	if f.I2CHeader.Alias != "/soc/i2c@7e804000" {
		t.Errorf("alias = %q, want the controller node recorded", f.I2CHeader.Alias)
	}
	if _, ok := f.I2CHeaderBus(); ok {
		t.Error("I2CHeaderBus() reported a bus that is not enabled")
	}
}

// With no alias at all we may only assume -- and must say so.
func TestI2CHeader_NoAliasAssumesAndWarns(t *testing.T) {
	f, _ := NewLinuxProber(pi5().host(), "test").Probe(context.Background())

	if f.I2CHeader.Source != I2CAssumed {
		t.Errorf("source = %q, want %q", f.I2CHeader.Source, I2CAssumed)
	}
	if f.I2CHeader.Path != "/dev/i2c-1" {
		t.Errorf("path = %q, want the conventional /dev/i2c-1", f.I2CHeader.Path)
	}
	if !hasWarning(f, "i2c.header.assumed") {
		t.Errorf("assumption not warned about; warnings = %+v", f.Warnings)
	}
}
