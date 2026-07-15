package check

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

// realPi4 is this project's reference host, captured from actual hardware.
func realPi4(t *testing.T) hostfacts.HostFacts {
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

func find(t *testing.T, rep Report, id string) Entry {
	t.Helper()
	for _, s := range rep.Sections {
		for _, e := range s.Results {
			if e.Check.ID == id {
				return e
			}
		}
	}
	t.Fatalf("check %q did not appear in the report", id)
	return Entry{}
}

// Every check that reports a problem must say what to do about it. A diagnosis
// with no fix leaves the user exactly where they started, which is the failure
// mode this whole package exists to avoid.
func TestEveryFailureCarriesAFix(t *testing.T) {
	f := realPi4(t)

	// Ask for everything, so the checks that refuse actually run.
	cfg := config.Default()
	cfg.Root = "/tmp/ws"
	cfg.Hardware = config.Hardware{GPIO: true, I2C: true, SPI: true, UART: true}

	rep := Run(f, &cfg, nil)
	if rep.Failed == 0 {
		t.Skip("nothing failed on the reference host; nothing to assert")
	}
	for _, s := range rep.Sections {
		for _, e := range s.Results {
			if e.Result.Status != Fail {
				continue
			}
			if e.Result.Fix == nil || len(e.Result.Fix.Steps) == 0 {
				t.Errorf("%s failed with no fix: %q\n"+
					"A check that says what is broken but not how to fix it has not helped.",
					e.Check.ID, e.Result.Value)
			}
		}
	}
}

// The i2c check is the one nothing else makes. A Pi with i2c off still shows
// several /dev/i2c-* nodes, so anything that globbed them would say i2c works.
func TestI2C_DistinguishesDecoyBusesFromTheHeader(t *testing.T) {
	f := realPi4(t)
	cfg := config.Default()
	cfg.Root = "/tmp/ws"
	cfg.Hardware.I2C = true

	e := find(t, Run(f, &cfg, nil), "hw.i2c")
	if e.Result.Status != Fail {
		t.Fatalf("i2c = %v on a Pi with i2c_arm unset, want fail", e.Result.Status)
	}

	body := strings.Join(e.Result.Detail, "\n")
	// The decoys must be named, or the user checks `ls /dev/i2c-*`, sees five
	// buses, and concludes ros2pi is wrong.
	if !strings.Contains(body, "/dev/i2c-0") {
		t.Errorf("the report does not mention the decoy buses:\n%s", body)
	}
	if !strings.Contains(body, "HDMI") {
		t.Errorf("the report does not explain what the decoys are:\n%s", body)
	}
	if e.Result.Fix == nil || !strings.Contains(fixText(e), "raspi-config") {
		t.Errorf("no runnable fix offered:\n%+v", e.Result.Fix)
	}
	if !e.Result.Fix.NeedsReboot {
		t.Error("enabling i2c needs a reboot and the fix should say so")
	}
}

// Not asking for i2c must not make an unconfigured Pi look broken.
func TestI2C_NotRequestedIsNotAFailure(t *testing.T) {
	f := realPi4(t)
	cfg := config.Default()
	cfg.Root = "/tmp/ws"
	cfg.Hardware.I2C = false

	if e := find(t, Run(f, &cfg, nil), "hw.i2c"); e.Result.Status == Fail {
		t.Error("i2c reported as a failure when the workspace never asked for it")
	}
}

// The two docker permission states produce identical output from docker and
// need opposite fixes. Telling them apart is the point.
func TestDockerGroup_StaleSessionSaysNewgrpNotUsermod(t *testing.T) {
	f := realPi4(t)
	f.Docker.Problem = hostfacts.DockerPermissionStaleSession
	for i := range f.Groups {
		if f.Groups[i].Name == "docker" {
			f.Groups[i].UserIsMember = true
			f.Groups[i].SessionHasGroup = false
		}
	}

	e := find(t, Run(f, nil, nil), "docker.group")
	if e.Result.Status != Fail {
		t.Fatalf("status = %v, want fail", e.Result.Status)
	}
	fix := fixText(e)
	if !strings.Contains(fix, "newgrp") {
		t.Errorf("the fix must be newgrp; got:\n%s", fix)
	}
	if strings.Contains(fix, "usermod") {
		t.Errorf("the fix must NOT be usermod -- the user is already in the group:\n%s", fix)
	}
}

func TestDockerGroup_GenuinelyNotAMemberSaysUsermod(t *testing.T) {
	f := realPi4(t)
	f.Docker.Problem = hostfacts.DockerPermissionDenied

	e := find(t, Run(f, nil, nil), "docker.group")
	if e.Result.Status != Fail {
		t.Fatalf("status = %v, want fail", e.Result.Status)
	}
	if !strings.Contains(fixText(e), "usermod") {
		t.Errorf("the fix must add the user to the group; got:\n%s", fixText(e))
	}
}

// A 32-bit userland under a 64-bit kernel is the classic Pi trap: uname says
// aarch64 and everything looks fine until docker cannot find a runnable image.
func TestArch_CatchesA32BitUserlandUnderA64BitKernel(t *testing.T) {
	f := realPi4(t)
	f.Arch.Dpkg = "armhf"

	e := find(t, Run(f, nil, nil), "host.arch")
	if e.Result.Status != Fail {
		t.Fatalf("status = %v, want fail: this host cannot run ROS 2 at all", e.Result.Status)
	}
	body := strings.Join(e.Result.Detail, " ")
	// It must be clear this is upstream, not us being unhelpful.
	if !strings.Contains(body, "armv7") || !strings.Contains(body, "upstream") {
		t.Errorf("the report should explain that this is closed upstream:\n%s", body)
	}
}

// Pi 5 support has never run on a Pi 5. Saying so where a Pi 5 user will see it
// is more useful than a line in the README they will not read.
func TestPi5_IsFlaggedAsUntested(t *testing.T) {
	f := realPi4(t)
	f.Model.Family = hostfacts.FamilyPi5
	f.Model.Raw = "Raspberry Pi 5 Model B Rev 1.0"
	for i := range f.GPIOChips {
		if f.GPIOChips[i].Header {
			f.GPIOChips[i].Label = "pinctrl-rp1"
		}
	}

	e := find(t, Run(f, nil, nil), "host.family")
	if e.Result.Status != Warn {
		t.Errorf("status = %v, want warn on a Pi 5", e.Result.Status)
	}
	if !strings.Contains(strings.Join(e.Result.Detail, " "), "never run on a real Pi 5") {
		t.Errorf("a Pi 5 user should be told this is untested:\n%v", e.Result.Detail)
	}
}

// check must work with no workspace: the moment a user needs it most is when
// nothing is set up yet.
func TestRun_WorksWithNoWorkspace(t *testing.T) {
	f := realPi4(t)
	rep := Run(f, nil, nil) // nil config

	if len(rep.Sections) == 0 {
		t.Fatal("no report produced without a workspace")
	}
	e := find(t, rep, "ws.root")
	if e.Result.Status == Fail {
		t.Error("having no workspace is not a failure; `ros2pi check` must run anywhere")
	}
}

// Every check ID is public API: --explain and bug reports name them.
func TestCheckIDsAreUniqueAndStable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Registry() {
		if seen[c.ID] {
			t.Errorf("duplicate check id %q", c.ID)
		}
		seen[c.ID] = true

		if c.ID == "" || c.Section == "" || c.Title == "" {
			t.Errorf("check %+v is missing an id, section or title", c)
		}
		if !strings.Contains(c.ID, ".") {
			t.Errorf("id %q should be namespaced, e.g. hw.i2c", c.ID)
		}
	}
}

func fixText(e Entry) string {
	if e.Result.Fix == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range e.Result.Fix.Steps {
		b.WriteString(s.Text + " " + s.Cmd + "\n")
	}
	return b.String()
}
