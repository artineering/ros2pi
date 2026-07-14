package dockerargs

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

func facts(t *testing.T) hostfacts.HostFacts {
	t.Helper()
	p := hostfacts.FileProber{
		FS:   os.DirFS("../hostfacts"),
		Path: "testdata/facts/pi4-k6.18-trixie.json",
	}
	f, err := p.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func ws(t *testing.T) config.Config {
	t.Helper()
	c := config.Default()
	c.Root = "/home/pi/ws"
	return c
}

func plan(t *testing.T, cfg config.Config, o Opts) Plan {
	t.Helper()
	p, err := CreateArgs(cfg, facts(t), o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The fingerprint decides whether a container is stale. It must therefore mean
// exactly "what this container IS" -- nothing more.
//
// Upgrading ros2pi does not change what a container is. When the version label
// was inside the hash, every upgrade marked every container on the machine
// stale, and the user was told to recreate -- stopping their nodes -- to apply
// a change that did not exist.
func TestFingerprint_IgnoresTheRos2piVersion(t *testing.T) {
	cfg := ws(t)

	a := plan(t, cfg, Opts{Version: "0.1.0", ShimDir: "/cache/shim/aaaa"})
	b := plan(t, cfg, Opts{Version: "0.9.9-rc1", ShimDir: "/cache/shim/aaaa"})

	if a.Fingerprint != b.Fingerprint {
		t.Errorf("a version bump changed the container's identity:\n  %s (0.1.0)\n  %s (0.9.9-rc1)\n"+
			"Upgrading ros2pi would mark every container stale for no reason.",
			a.Fingerprint, b.Fingerprint)
	}
	// It must still be recorded: a bug report needs to know which ros2pi built
	// the container.
	if !strings.Contains(strings.Join(b.Args, " "), LabelVersion+"=0.9.9-rc1") {
		t.Error("the version is not recorded as a label at all")
	}
}

// The shim decides how ROS is sourced and whether a wrong distro is caught, so
// a changed shim IS a changed container. When it lived outside the hash, a fix
// to the entry script silently never reached anyone who already had a
// container.
func TestFingerprint_ChangesWithTheShim(t *testing.T) {
	cfg := ws(t)

	a := plan(t, cfg, Opts{Version: "0.1.0", ShimDir: "/cache/shim/aaaa"})
	b := plan(t, cfg, Opts{Version: "0.1.0", ShimDir: "/cache/shim/bbbb"})

	if a.Fingerprint == b.Fingerprint {
		t.Error("a changed shim did not mark the container stale; a fix to entry.sh " +
			"would never reach an existing container")
	}
	if !strings.Contains(strings.Join(a.Args, " "), "/cache/shim/aaaa:"+ShimDir+":ro") {
		t.Error("the shim is not mounted read-only into the container")
	}
}

// Things that genuinely change the container must change its identity.
func TestFingerprint_ChangesWithRealConfiguration(t *testing.T) {
	base := ws(t)
	o := Opts{Version: "0.1.0", ShimDir: "/cache/shim/aaaa"}
	orig := plan(t, base, o).Fingerprint

	t.Run("image", func(t *testing.T) {
		c := ws(t)
		c.ROS.Image = "ros2pi-ws:latest"
		if plan(t, c, o).Fingerprint == orig {
			t.Error("changing the image did not change the fingerprint")
		}
	})
	t.Run("domain id", func(t *testing.T) {
		c := ws(t)
		c.ROS.DomainID = 7
		if plan(t, c, o).Fingerprint == orig {
			t.Error("changing ROS_DOMAIN_ID did not change the fingerprint")
		}
	})
	t.Run("hardware", func(t *testing.T) {
		c := ws(t)
		c.Hardware.GPIO = true
		if plan(t, c, o).Fingerprint == orig {
			t.Error("requesting GPIO did not change the fingerprint")
		}
	})
	t.Run("distro", func(t *testing.T) {
		c := ws(t)
		c.ROS.Distro = "humble"
		c.ROS.Image = "ros:humble"
		if plan(t, c, o).Fingerprint == orig {
			t.Error("changing the distro did not change the fingerprint")
		}
	})
}

// Editing settings that never reach the container must NOT force a recreate:
// being told to stop your nodes to apply a colcon flag would be absurd.
func TestFingerprint_IgnoresSettingsTheContainerNeverSees(t *testing.T) {
	o := Opts{Version: "0.1.0", ShimDir: "/cache/shim/aaaa"}
	orig := plan(t, ws(t), o).Fingerprint

	c := ws(t)
	c.Build.ColconArgs = []string{"--symlink-install", "--cmake-args", "-DFOO=1"}
	c.Build.ParallelWorkers = 2

	if plan(t, c, o).Fingerprint != orig {
		t.Error("a [build] setting changed the container's identity; those flags are " +
			"passed to colcon at exec time and never reach docker create")
	}
}

// The same inputs must always produce the same argv, or the fingerprint flaps
// and a healthy container intermittently looks stale.
func TestCreateArgs_IsDeterministic(t *testing.T) {
	cfg := ws(t)
	cfg.Hardware = config.Hardware{GPIO: true, USBSerial: true}
	cfg.Env = map[string]string{"B": "2", "A": "1", "C": "3"}
	cfg.Mounts.Extra = []string{"/data:/data:ro"}
	o := Opts{Version: "0.1.0", ShimDir: "/cache/shim/aaaa"}

	first := plan(t, cfg, o)
	for i := 0; i < 50; i++ {
		got := plan(t, cfg, o)
		if strings.Join(got.Args, "\x00") != strings.Join(first.Args, "\x00") {
			t.Fatalf("argv flapped between runs:\n  %v\n  %v", first.Args, got.Args)
		}
		if got.Fingerprint != first.Fingerprint {
			t.Fatalf("fingerprint flapped: %s vs %s", first.Fingerprint, got.Fingerprint)
		}
	}
}
