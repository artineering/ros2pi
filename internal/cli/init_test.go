package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initApp is an App whose output can be read back.
func initApp(t *testing.T, stdin string) (App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errb bytes.Buffer
	return App{
		Version: "test",
		Stdin:   strings.NewReader(stdin),
		Stdout:  &out,
		Stderr:  &errb,
		IsTTY:   func() bool { return false }, // no prompt; take the default
	}, &out, &errb
}

func runInit(t *testing.T, dir string, args ...string) (string, string) {
	t.Helper()
	a, out, errb := initApp(t, "")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	in, err := Route(append([]string{"init"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.dispatch(context.Background(), in); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	return out.String(), errb.String()
}

// What `init` prints is the first thing anyone reads, and for a long time it
// told them to pass --destination-directory -- a flag ros2pi had started
// adding for them. That is worse than noise: it teaches a command that is not
// needed, and implies the tool will not do it.
//
// Nothing caught it because App wrote to *os.File and no test could read the
// output. That is why the streams are interfaces now.
func TestInit_DoesNotTellUsersToPassAFlagWeAddOurselves(t *testing.T) {
	out, _ := runInit(t, t.TempDir())

	if strings.Contains(out, destFlag) {
		t.Errorf("init tells the user to pass %s, which ros2pi adds automatically:\n%s",
			destFlag, out)
	}
	// The suggestion must still be a command that works.
	if !strings.Contains(out, "ros2pi pkg create") {
		t.Errorf("init no longer suggests how to create a package:\n%s", out)
	}
}

// Everything init suggests must be a real ros2pi command, or the first thing a
// new user types fails.
func TestInit_SuggestsOnlyRealCommands(t *testing.T) {
	out, _ := runInit(t, t.TempDir())

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ros2pi ") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "ros2pi "))
		if len(fields) == 0 {
			continue
		}
		verb := fields[0]

		// Either one of ours, or something ros2 understands and we forward.
		if _, ours := ownVerbs[verb]; ours {
			continue
		}
		if ros2Verbs[verb] {
			continue
		}
		t.Errorf("init suggests `ros2pi %s`, which is neither a ros2pi command "+
			"nor a ros2 one:\n  %s", verb, line)
	}
}

func TestInit_WritesTheChosenDistro(t *testing.T) {
	dir := t.TempDir()
	out, _ := runInit(t, dir, "--distro", "humble")

	body, err := os.ReadFile(filepath.Join(dir, "ros2pi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "distro = 'humble'") {
		t.Errorf("ros2pi.toml does not record the chosen distro:\n%s", body)
	}
	if !strings.Contains(string(body), "image = 'ros:humble'") {
		t.Errorf("the image should follow the distro:\n%s", body)
	}
	if !strings.Contains(out, "humble") {
		t.Errorf("init should say what it chose:\n%s", out)
	}
}

// Without a terminal there is nobody to ask, so the default is used -- but it
// must never be silent, or a script gets a distro nobody picked.
func TestInit_NonInteractiveSaysWhatItChose(t *testing.T) {
	_, errOut := runInit(t, t.TempDir())

	if !strings.Contains(errOut, "no terminal") {
		t.Errorf("init should say why it did not ask:\n%s", errOut)
	}
	if !strings.Contains(errOut, "--distro") {
		t.Errorf("init should say how to override it:\n%s", errOut)
	}
}

// build/install/log hold binaries linked against the container's ROS: they are
// host files that will not run on the host, and committing them is never right.
func TestInit_WritesAGitignore(t *testing.T) {
	dir := t.TempDir()
	runInit(t, dir)

	body, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("no .gitignore written: %v", err)
	}
	for _, d := range []string{"build/", "install/", "log/"} {
		if !strings.Contains(string(body), d) {
			t.Errorf(".gitignore does not exclude %s:\n%s", d, body)
		}
	}
}
