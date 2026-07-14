//go:build integration

// Package e2e drives the real ros2pi binary against real Docker.
//
// These tests are tagged `integration` and excluded from `go test ./...`,
// because they need Docker, a ~1.3 GB ROS image, and a few minutes. Run them
// with:
//
//	go test -tags integration ./test/e2e/ -v
//
// Everything here was verified by hand while building the tool. That was the
// problem: a check that lives only in a terminal history proves nothing a week
// later. These are the same checks, written down so they can fail.
//
// They deliberately assert on OBSERVABLE BEHAVIOUR -- what a user types and
// what comes back -- not on internals. The unit tests already cover the
// reasoning; this covers whether the reasoning survives contact with Docker.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The milestone gate: a workspace goes from nothing to a running node.
func TestInitBuildRun(t *testing.T) {
	ws := workspace(t)

	run(t, ws, "pkg", "create", "--build-type", "ament_python",
		"--node-name", "my_node", "my_pkg")

	if out := run(t, ws, "build"); !strings.Contains(out, "1 package finished") {
		t.Errorf("build did not report a finished package:\n%s", out)
	}

	out := run(t, ws, "run", "my_pkg", "my_node")
	if !strings.Contains(out, "Hi from my_pkg") {
		t.Errorf("the node did not run:\n%s", out)
	}
}

// Packages must land in src/. `ros2 pkg create foo` from a workspace root makes
// ./foo, colcon builds it anyway, and nothing tells you the layout is wrong --
// so ros2pi adds the flag, and must keep doing so.
func TestPkgCreateLandsInSrc(t *testing.T) {
	ws := workspace(t)
	out := run(t, ws, "pkg", "create", "--build-type", "ament_python",
		"--node-name", "n", "my_pkg")

	if _, err := os.Stat(filepath.Join(ws, "src", "my_pkg")); err != nil {
		t.Errorf("package is not in src/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "my_pkg")); err == nil {
		t.Error("package was created at the workspace root")
	}
	// Rewriting a command silently would be worse than the bug it prevents.
	if !strings.Contains(out, "src/") {
		t.Errorf("ros2pi changed the command without saying so:\n%s", out)
	}
}

// An explicit destination must always win over our default.
func TestPkgCreateRespectsExplicitDestination(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "pkg", "create", "--build-type", "ament_python", "--node-name", "n",
		"--destination-directory", ".", "top_pkg")

	if _, err := os.Stat(filepath.Join(ws, "top_pkg")); err != nil {
		t.Errorf("explicit --destination-directory . was ignored: %v", err)
	}
}

// Files written by the container must be owned by the user, not root. This is
// the bug the whole tool exists to prevent, so it is worth an end-to-end check
// rather than trusting the --user flag to mean what we think.
func TestBuildArtefactsAreOwnedByTheUser(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "pkg", "create", "--build-type", "ament_python",
		"--node-name", "n", "my_pkg")
	run(t, ws, "build")

	for _, d := range []string{"build", "install", "log", "src/my_pkg"} {
		p := filepath.Join(ws, d)
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("%s missing: %v", d, err)
			continue
		}
		// Writable by us is the property that actually matters; a root-owned
		// tree is exactly what users cannot clean up afterwards.
		f, err := os.CreateTemp(p, ".owned-*")
		if err != nil {
			t.Errorf("%s is not writable by the invoking user (mode %v): %v", d, fi.Mode(), err)
			continue
		}
		f.Close()
		os.Remove(f.Name())
	}
}

// Asking for one ROS version and pointing at an image containing another must
// FAIL. Before the shim it silently succeeded: the image's entrypoint sources
// its own distro first, so the user got that one and was never told.
func TestWrongDistroRefusesInsteadOfSilentlyWorking(t *testing.T) {
	ws := workspace(t)

	cfg := filepath.Join(ws, "ros2pi.toml")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Ask for humble while the image is ros:jazzy.
	if err := os.WriteFile(cfg,
		[]byte(strings.Replace(string(body), "distro = 'jazzy'", "distro = 'humble'", 1)),
		0o644); err != nil {
		t.Fatal(err)
	}

	out, code := try(t, ws, "topic", "list")
	if code == 0 {
		t.Fatalf("a humble workspace ran against a jazzy image and said nothing:\n%s", out)
	}
	if !strings.Contains(out, "humble") || !strings.Contains(out, "jazzy") {
		t.Errorf("the error should name BOTH versions so the mismatch is obvious:\n%s", out)
	}
}

// A failing ros2 command must fail ros2pi with a non-zero code, or scripts and
// CI that wrap it would silently pass.
func TestExitCodePropagates(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "up")

	if _, code := try(t, ws, "run", "no_such_pkg", "no_such_node"); code == 0 {
		t.Error("a failing ros2 command returned success")
	}
	if out, code := try(t, ws, "topic", "list"); code != 0 {
		t.Errorf("a working ros2 command returned %d:\n%s", code, out)
	}
}

// Commands we do not own must reach ros2 untouched, including their flags.
func TestPassthroughReachesROS2(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "up")

	if out := run(t, ws, "topic", "list"); !strings.Contains(out, "/rosout") {
		t.Errorf("ros2 topic list did not run:\n%s", out)
	}
	// `ros2 doctor` is a real command; ours is `check` precisely so this works.
	if _, code := try(t, ws, "doctor", "--help"); code != 0 {
		t.Error("`ros2pi doctor` did not reach ros2; something is shadowing it")
	}
}

// Editing the config while the container runs must refuse rather than silently
// recreate: the container may own devices or be recording a bag.
func TestStaleConfigRefusesToRecreateSilently(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "up")

	cfg := filepath.Join(ws, "ros2pi.toml")
	body, _ := os.ReadFile(cfg)
	_ = os.WriteFile(cfg,
		[]byte(strings.Replace(string(body), "domain_id = 0", "domain_id = 7", 1)), 0o644)

	out, code := try(t, ws, "topic", "list")
	if code == 0 {
		t.Fatalf("a config change was applied silently to a running container:\n%s", out)
	}
	if !strings.Contains(out, "--recreate") {
		t.Errorf("the error must tell the user how to proceed:\n%s", out)
	}
	// And the command it recommends must actually work.
	run(t, ws, "up", "--recreate")
	run(t, ws, "topic", "list")
}

// Dependencies declared in package.xml must survive the container being
// recreated. This is the entire reason `ros2pi image build` exists: installing
// them into a live container works and then loses them on the next recreate,
// leaving a build broken by a missing module the user knows they installed.
func TestImageBuildDependenciesSurviveRecreate(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "pkg", "create", "--build-type", "ament_python",
		"--node-name", "n", "dep_pkg")

	// Declare a dependency that is NOT in the base image, so its presence later
	// can only be explained by the image build.
	manifest := filepath.Join(ws, "src", "dep_pkg", "package.xml")
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(body),
		"<test_depend>", "<depend>python3-serial</depend>\n  <test_depend>", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Not present before the build: without this the test could pass on a base
	// image that happened to ship it.
	if _, code := try(t, ws, "shell", "-c", "python3 -c 'import serial'"); code == 0 {
		t.Skip("python3-serial is already in the base image; pick another dependency")
	}

	run(t, ws, "image", "build")
	run(t, ws, "up", "--recreate")

	if out := run(t, ws, "shell", "-c",
		"python3 -c 'import serial; print(serial.__version__)'"); !strings.Contains(out, "3.") {
		t.Fatalf("the dependency is not in the image:\n%s", out)
	}

	// The point: recreate destroys the container. The dependency must remain,
	// because it is in the image rather than in the container.
	run(t, ws, "up", "--recreate")
	if out, code := try(t, ws, "shell", "-c", "python3 -c 'import serial'"); code != 0 {
		t.Fatalf("the dependency vanished on recreate -- it was installed into the "+
			"container, not baked into the image:\n%s", out)
	}
}

// `ros2pi image build` renames the workspace image to match the container name.
// docker inspect searches containers AND images, so a bare inspect resolves to
// that image once the container is gone -- and ros2pi must still recover.
func TestRecoversAfterContainerRemovedBehindItsBack(t *testing.T) {
	ws := workspace(t)
	run(t, ws, "up")

	out, _ := exec.Command("docker", "ps", "-aq",
		"--filter", "label=io.ros2pi.workspace="+ws).Output()
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		t.Fatal("no container to remove")
	}
	if err := exec.Command("docker", "rm", "-f", ids[0]).Run(); err != nil {
		t.Fatal(err)
	}

	// A removed container is a normal state, not an error: ros2pi must make a
	// new one rather than report a docker failure the user cannot act on.
	if out := run(t, ws, "topic", "list"); !strings.Contains(out, "/rosout") {
		t.Errorf("could not recover from a removed container:\n%s", out)
	}
}
