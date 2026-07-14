package cli

import (
	"strings"
	"testing"
)

func TestRoute(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		mode Mode
		verb string
		pass []string
		own  []string
	}{
		{
			name: "bare ros2pi shows help",
			argv: nil,
			mode: ModeHelp,
		},
		{
			name: "our own verb",
			argv: []string{"build"},
			mode: ModeOwn, verb: "build",
		},
		{
			// The core promise: anything we do not own reaches ros2 untouched.
			name: "unknown verb goes to ros2",
			argv: []string{"topic", "list"},
			mode: ModePassthrough, pass: []string{"topic", "list"},
		},
		{
			// `ros2 doctor` is real. Ours is `check`, precisely so this works.
			name: "ros2 doctor is not shadowed",
			argv: []string{"doctor"},
			mode: ModePassthrough, pass: []string{"doctor"},
		},
		{
			name: "flags after a passthrough verb belong to ros2",
			argv: []string{"topic", "pub", "--once", "/chatter", "std_msgs/String"},
			mode: ModePassthrough,
			pass: []string{"topic", "pub", "--once", "/chatter", "std_msgs/String"},
		},
		{
			// --verbose is OURS here because it precedes the verb.
			name: "our flags before the verb are ours",
			argv: []string{"--verbose", "topic", "list"},
			mode: ModePassthrough, pass: []string{"topic", "list"},
		},
		{
			// ...and identical text AFTER a passthrough verb is ros2's.
			name: "the same flag after the verb is not ours",
			argv: []string{"topic", "list", "--verbose"},
			mode: ModePassthrough, pass: []string{"topic", "list", "--verbose"},
		},
		{
			// The form users actually type, and the form our own errors print.
			name: "our flags after OUR verb are still ours",
			argv: []string{"up", "--recreate"},
			mode: ModeOwn, verb: "up",
		},
		{
			name: "unknown flags after our verb pass to the underlying tool",
			argv: []string{"build", "--packages-select", "my_pkg"},
			mode: ModeOwn, verb: "build",
			own: []string{"--packages-select", "my_pkg"},
		},
		{
			// The escape hatch for the day ros2 adds a verb named like ours.
			name: "-- forces passthrough",
			argv: []string{"--", "build", "--merge-install"},
			mode: ModePassthrough, pass: []string{"build", "--merge-install"},
		},
		{
			name: "-- after our verb separates our flags from the command's",
			argv: []string{"build", "--verbose", "--", "--cmake-args", "-DFOO=1"},
			mode: ModeOwn, verb: "build",
			own: []string{"--cmake-args", "-DFOO=1"},
		},
		{
			name: "help",
			argv: []string{"--help"},
			mode: ModeHelp,
		},
		{
			name: "version",
			argv: []string{"--version"},
			mode: ModeVersion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, err := Route(tc.argv)
			if err != nil {
				t.Fatalf("Route(%v) errored: %v", tc.argv, err)
			}
			if in.Mode != tc.mode {
				t.Errorf("mode = %v, want %v", in.Mode, tc.mode)
			}
			if tc.verb != "" && in.Verb != tc.verb {
				t.Errorf("verb = %q, want %q", in.Verb, tc.verb)
			}
			if tc.pass != nil && strings.Join(in.PassArgs, " ") != strings.Join(tc.pass, " ") {
				t.Errorf("passthrough = %v, want %v", in.PassArgs, tc.pass)
			}
			if tc.own != nil && strings.Join(in.OwnArgs, " ") != strings.Join(tc.own, " ") {
				t.Errorf("own args = %v, want %v", in.OwnArgs, tc.own)
			}
		})
	}
}

func TestRoute_GlobalsBeforeVerb(t *testing.T) {
	in, err := Route([]string{"-C", "/tmp/ws", "--dry-run", "--verbose", "up"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Globals.Workspace != "/tmp/ws" || !in.Globals.DryRun || !in.Globals.Verbose {
		t.Errorf("globals = %+v, want workspace=/tmp/ws, dry-run, verbose", in.Globals)
	}
	if in.Verb != "up" {
		t.Errorf("verb = %q, want up", in.Verb)
	}
}

func TestRoute_GlobalsAfterOurVerb(t *testing.T) {
	in, err := Route([]string{"up", "--recreate", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !in.Globals.Recreate || !in.Globals.DryRun {
		t.Errorf("globals = %+v, want recreate and dry-run", in.Globals)
	}
}

// An unknown flag BEFORE the verb cannot be for ros2 (ros2 has not been named
// yet), so it is our error -- and the message must say how to reach ros2.
func TestRoute_UnknownLeadingFlagExplainsItself(t *testing.T) {
	_, err := Route([]string{"--nonsense", "topic"})
	if err == nil {
		t.Fatal("expected an error for an unknown leading flag")
	}
	if !strings.Contains(err.Error(), "--") {
		t.Errorf("error should point at the `--` escape hatch, got: %v", err)
	}
}

func TestRoute_BareDashDashIsAnError(t *testing.T) {
	if _, err := Route([]string{"--"}); err == nil {
		t.Fatal("`ros2pi --` with no command should error, not run something arbitrary")
	}
}

// A verb of ours that shadows a real ros2 command silently breaks passthrough
// for every user of it. Fail the build instead.
func TestOwnVerbsDoNotShadowROS2(t *testing.T) {
	if bad := CollidesWithROS2(); len(bad) > 0 {
		t.Fatalf("these ros2pi verbs shadow real ros2 commands: %v\n"+
			"Rename them: `ros2pi %s` must reach ros2.", bad, bad[0])
	}
}
