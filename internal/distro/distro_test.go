package distro

import "testing"

// The built-in list will go stale -- ROS releases every May. What must never
// happen is offering a distribution that is dead: this list was first written
// from memory and included Iron, which has been end-of-life since 2024, while
// omitting Lyrical entirely. Both were caught by reading ros/rosdistro rather
// than trusting the author.
func TestKnownDistrosAreNotEndOfLife(t *testing.T) {
	// Verified end-of-life against ros/rosdistro index-v4.yaml on 2026-07-15.
	dead := map[string]bool{
		"ardent": true, "bouncy": true, "crystal": true, "dashing": true,
		"eloquent": true, "foxy": true, "galactic": true, "iron": true,
	}
	for _, d := range All() {
		if dead[d.Name] {
			t.Errorf("%s is end-of-life and must not be offered.\n"+
				"Check before changing this list:\n"+
				"  curl -s https://raw.githubusercontent.com/ros/rosdistro/master/index-v4.yaml",
				d.Name)
		}
	}
}

// The default is what a user gets when they do not choose, including in every
// script and CI job. It must be the LTS with the longest support.
func TestDefaultIsAnLTS(t *testing.T) {
	d, known := Lookup(Default)
	if !known {
		t.Fatalf("the default %q is not in the built-in list", Default)
	}
	if d.Support != LTS {
		t.Errorf("default %q is %q; a default that expires soon is a trap", Default, d.Support)
	}
}

// An unknown distro is expected -- ROS ships one every year and this build will
// predate it -- so it must be accepted, not refused.
func TestUnknownDistroIsAllowedAndExplained(t *testing.T) {
	d, known := Lookup("newthing")
	if known {
		t.Fatal("a made-up distro was reported as known")
	}
	if d.Name != "newthing" {
		t.Errorf("Lookup should still return the name, got %q", d.Name)
	}
	if UnknownNote("newthing") == "" {
		t.Error("an unknown distro must be explained, not silently accepted")
	}
}

// Every offered distro needs the facts a person needs to choose.
func TestEveryDistroIsDescribed(t *testing.T) {
	for _, d := range All() {
		if d.Describe() == "" {
			t.Errorf("%s has no description", d.Name)
		}
		if d.Support != Development && d.EOL == "" {
			t.Errorf("%s has no EOL year; that is the main thing a user is choosing on", d.Name)
		}
		if Image(d.Name) != "ros:"+d.Name {
			t.Errorf("unexpected image for %s: %s", d.Name, Image(d.Name))
		}
	}
}
