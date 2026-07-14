//go:build integration || hardware

// Shared harness for the tagged end-to-end tests.
//
// Guarded by `integration || hardware` so either suite can run on its own:
// hardware tests need a real Pi but not the full integration set, and vice
// versa. Untagged, this file compiles into nothing, so `go test ./...` never
// builds a binary or touches Docker.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ros2pi-e2e-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binary = filepath.Join(dir, "ros2pi")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/ros2pi")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("build ros2pi: " + err.Error())
	}
	os.Exit(m.Run())
}

// requireDocker skips rather than fails when Docker is unusable: these tests
// are meant to be runnable from a laptop checkout without pretending the
// environment is broken.
func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker is not usable here (need the docker group? try: newgrp docker)")
	}
}

// workspace makes a throwaway ros2pi workspace and guarantees its container is
// removed afterwards, however the test ends.
func workspace(t *testing.T) string {
	t.Helper()
	requireDocker(t)

	dir := t.TempDir()
	run(t, dir, "init")

	t.Cleanup(func() {
		// Find the container by the label ros2pi stamps on it, so cleanup does
		// not depend on reproducing the name derivation.
		out, _ := exec.Command("docker", "ps", "-aq",
			"--filter", "label=io.ros2pi.workspace="+dir).Output()
		for _, id := range strings.Fields(string(out)) {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
	})
	return dir
}

// run executes ros2pi in dir and fails the test if it errors.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, code := try(t, dir, args...)
	if code != 0 {
		t.Fatalf("ros2pi %s failed (exit %d):\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// try executes ros2pi and returns its combined output and exit code, without
// failing the test -- for the cases where a non-zero exit IS the assertion.
func try(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if ok := asExitError(err, &ee); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("running ros2pi %v: %v", args, err)
	return "", -1
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}
