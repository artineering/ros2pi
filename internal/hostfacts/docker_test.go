package hostfacts

import (
	"context"
	"strings"
	"testing"
)

// dockerOK is the real `docker info --format` output shape from this Pi.
const dockerOK = "29.6.1|aarch64|2|[name=seccomp,profile=builtin]"

func probeDocker(t *testing.T, p fakePi) DockerFacts {
	t.Helper()
	f, err := NewLinuxProber(p.host(), "test").Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f.Docker
}

func TestDocker_Healthy(t *testing.T) {
	p := pi5()
	p.dockerOut = dockerOK
	d := probeDocker(t, p)

	if !d.Usable() {
		t.Fatalf("docker = %+v, want usable", d)
	}
	if d.ServerVersion != "29.6.1" || d.ServerArch != "aarch64" || d.CgroupVersion != "2" {
		t.Errorf("parsed = %+v, want 29.6.1/aarch64/cgroup2", d)
	}
	if d.Rootless {
		t.Error("rootless = true, want false")
	}
}

func TestDocker_BinaryAbsent(t *testing.T) {
	p := pi5()
	p.noDocker = true
	d := probeDocker(t, p)

	if d.Problem != DockerAbsent {
		t.Errorf("problem = %q, want %q", d.Problem, DockerAbsent)
	}
	if d.Binary != "" {
		t.Errorf("binary = %q, want empty", d.Binary)
	}
}

func TestDocker_DaemonUnreachable(t *testing.T) {
	p := pi5()
	p.dockerCode = 1
	p.dockerErr = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. " +
		"Is the docker daemon running?"
	if d := probeDocker(t, p); d.Problem != DockerDaemonUnreachable {
		t.Errorf("problem = %q, want %q", d.Problem, DockerDaemonUnreachable)
	}
}

// The headline case. These two produce IDENTICAL stderr and need opposite
// fixes, so only the group database/session disagreement can tell them apart.
func TestDocker_PermissionDenied_StaleSessionVsGenuine(t *testing.T) {
	const permErr = "permission denied while trying to connect to the Docker daemon socket at " +
		"unix:///var/run/docker.sock: Get \"http://%2Fvar%2Frun%2Fdocker.sock/v1.51/info\": " +
		"dial unix /var/run/docker.sock: connect: permission denied"

	t.Run("user is in the group but this session predates it", func(t *testing.T) {
		p := pi5()
		p.dockerCode, p.dockerErr = 1, permErr
		p.groups = map[string]string{"docker": "984", "gpio": "993"}
		// The database says member; the session does not carry it.
		p.sessionGroup = map[string]bool{"gpio": true}

		d := probeDocker(t, p)
		if d.Problem != DockerPermissionStaleSession {
			t.Errorf("problem = %q, want %q: usermod ran but the shell is older",
				d.Problem, DockerPermissionStaleSession)
		}
	})

	t.Run("user is genuinely not in the group", func(t *testing.T) {
		p := pi5()
		p.dockerCode, p.dockerErr = 1, permErr
		p.groups = map[string]string{"gpio": "993"} // no docker group membership at all
		p.sessionGroup = map[string]bool{"gpio": true}

		d := probeDocker(t, p)
		if d.Problem != DockerPermissionDenied {
			t.Errorf("problem = %q, want %q", d.Problem, DockerPermissionDenied)
		}
	})
}

// Docker's wording has drifted across versions ("Docker API" vs "Docker daemon
// socket"); both must classify.
func TestDocker_PermissionWordingVariants(t *testing.T) {
	for _, msg := range []string{
		"permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock",
		"permission denied while trying to connect to the docker API at unix:///var/run/docker.sock",
	} {
		p := pi5()
		p.dockerCode, p.dockerErr = 1, msg
		p.groups = map[string]string{"gpio": "993"}
		p.sessionGroup = map[string]bool{"gpio": true}
		if d := probeDocker(t, p); d.Problem != DockerPermissionDenied {
			t.Errorf("stderr %q classified as %q, want permission_denied", msg, d.Problem)
		}
	}
}

// An unrecognised failure must NOT be forced into a bucket -- that produces
// confidently wrong advice. Keep the raw text so a bug report can teach us.
func TestDocker_UnknownFailurePreservesStderr(t *testing.T) {
	p := pi5()
	p.dockerCode = 1
	p.dockerErr = "Error response from daemon: something nobody has seen before"
	d := probeDocker(t, p)

	if d.Problem != DockerUnknownFailure {
		t.Errorf("problem = %q, want %q", d.Problem, DockerUnknownFailure)
	}
	if !strings.Contains(d.Detail, "nobody has seen before") {
		t.Errorf("detail = %q, want the raw stderr preserved", d.Detail)
	}
}

// Rootless docker has no device-cgroup control, so hardware passthrough cannot
// work there. Detecting it is what lets `check` say so instead of letting the
// user discover it as an EPERM at open() time.
func TestDocker_RootlessDetected(t *testing.T) {
	p := pi5()
	p.dockerOut = "29.6.1|aarch64|2|[name=seccomp,profile=builtin name=rootless]"
	d := probeDocker(t, p)

	if !d.Usable() {
		t.Fatalf("docker = %+v, want usable", d)
	}
	if !d.Rootless {
		t.Error("rootless docker not detected; hardware passthrough would fail at open()")
	}
}

// The client version is available even when the daemon is not, which is exactly
// when a bug report needs it.
func TestDocker_ClientVersionSurvivesDaemonFailure(t *testing.T) {
	p := pi5()
	p.dockerCode, p.dockerErr = 1, "Cannot connect to the Docker daemon"
	if d := probeDocker(t, p); d.ClientVersion != "29.6.1" {
		t.Errorf("client version = %q, want it recorded despite the daemon being down", d.ClientVersion)
	}
}

// Nothing may claim a stale session when the group does not exist at all.
func TestGroup_MembershipIsStale(t *testing.T) {
	cases := []struct {
		name string
		g    Group
		want bool
	}{
		{"added but session is older", Group{Exists: true, UserIsMember: true, SessionHasGroup: false}, true},
		{"fully in effect", Group{Exists: true, UserIsMember: true, SessionHasGroup: true}, false},
		{"not a member", Group{Exists: true, UserIsMember: false, SessionHasGroup: false}, false},
		{"group absent", Group{Exists: false, UserIsMember: true, SessionHasGroup: false}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.g.MembershipIsStale(); got != tc.want {
				t.Errorf("MembershipIsStale() = %v, want %v", got, tc.want)
			}
		})
	}
}
