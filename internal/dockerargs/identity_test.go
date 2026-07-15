package dockerargs

import (
	"strings"
	"testing"
)

// The image may have no user at our uid, and we must not care.
//
// ros:jazzy (Ubuntu 24.04) ships an `ubuntu` user at uid 1000, so running
// --user 1000 there works by luck. ros:humble (Ubuntu 22.04) has nobody at
// 1000, and that broke two things: getpwuid() failed, so `ros2 pkg create`
// died with "KeyError: getpwuid(): uid not found: 1000"; and HOME was /, which
// is not writable.
//
// Supplying the identity ourselves is what makes ros2pi behave the same on
// every image instead of on one by accident.
func TestCreateArgs_SuppliesIdentityTheImageMayNotHave(t *testing.T) {
	p := plan(t, ws(t), Opts{Version: "0.1.0", ShimDir: "/cache/shim/a"})
	args := strings.Join(p.Args, " ")

	// getpass.getuser() reads USER/LOGNAME before falling back to /etc/passwd,
	// which is what lets a uid with no passwd entry still answer "who am I".
	for _, want := range []string{"USER=", "LOGNAME="} {
		if !strings.Contains(args, want) {
			t.Errorf("no %s in the environment; `ros2 pkg create` crashes on an image "+
				"with no user at our uid:\n  %s", want, args)
		}
	}
	if !strings.Contains(args, "HOME="+ContainerHome) {
		t.Errorf("HOME is not set; images that give this uid no home leave it at /, "+
			"which is not writable:\n  %s", args)
	}
}

// The username comes from the host, so container-side logs and bag metadata
// name the person who actually ran it.
func TestCreateArgs_UsesTheHostUsername(t *testing.T) {
	f := facts(t)
	f.Identity.Username = "pi"

	p, err := CreateArgs(ws(t), f, Opts{Version: "0.1.0", ShimDir: "/cache/shim/a"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(p.Args, " ")
	if !strings.Contains(args, "USER=pi") {
		t.Errorf("USER should be the host username:\n  %s", args)
	}
}

// A host with no resolvable username must still produce a usable container
// rather than USER= with nothing after it.
func TestCreateArgs_FallsBackWhenTheUsernameIsUnknown(t *testing.T) {
	f := facts(t)
	f.Identity.Username = ""

	p, err := CreateArgs(ws(t), f, Opts{Version: "0.1.0", ShimDir: "/cache/shim/a"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(p.Args, " ")
	if strings.Contains(args, "USER= ") || strings.HasSuffix(args, "USER=") {
		t.Errorf("USER is empty; tools reading it would get nothing:\n  %s", args)
	}
	if !strings.Contains(args, "USER=ros2pi") {
		t.Errorf("expected a fallback username:\n  %s", args)
	}
}
