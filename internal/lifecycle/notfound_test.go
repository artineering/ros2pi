package lifecycle

import "testing"

// Docker says "container is not there" in more than one way, and the wording
// differs by subcommand. Matching only one spelling meant `inspect` failures
// were treated as real errors, so ros2pi could not recover from a container
// that had simply been removed.
func TestIsNotFound(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"Error response from daemon: No such container: ros2pi-ws-1234", true},
		{"Error: No such object: ros2pi-ws-1234", true},
		{"Error response from daemon: no such container: x", true}, // case varies
		{"", false},
		// A real failure must NOT be mistaken for absence, or ros2pi would
		// cheerfully try to create a container on a broken daemon.
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock", false},
		{"permission denied while trying to connect to the Docker daemon socket", false},
	}
	for _, tc := range cases {
		if got := isNotFound(tc.stderr); got != tc.want {
			t.Errorf("isNotFound(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
}
