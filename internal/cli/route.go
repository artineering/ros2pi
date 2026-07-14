// Package cli routes argv.
//
// Routing happens BEFORE any flag library sees the arguments. A flag parser
// would mangle `ros2pi topic pub --once`, and fighting it with
// DisableFlagParsing gets to the same place with more machinery. Route is a
// pure function over argv, which also makes it exhaustively table-testable.
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// Mode is what an invocation turns out to be.
type Mode int

const (
	ModeOwn Mode = iota
	ModePassthrough
	ModeHelp
	ModeVersion
)

// ownVerbs are ros2pi's own commands.
//
// Every name here is chosen NOT to collide with a real ros2 verb, so that
// `ros2pi <anything ros2 knows>` reaches ros2 untouched. Notably ours is
// `check`, never `doctor`: `ros2 doctor` is a real command and shadowing it
// would be a nasty surprise.
var ownVerbs = map[string]string{
	"init":  "create a ros2pi workspace here",
	"up":    "create and start the workspace container",
	"down":  "stop the workspace container",
	"build": "run colcon build in the container",
	"shell": "open a shell in the container",
	"check": "diagnose this Pi and the workspace",
	"image": "manage the workspace image",
	"setup": "install and configure docker on this Pi",
}

// ros2Verbs are the built-in ros2 commands, recorded so that Route can reject a
// future collision loudly instead of silently shadowing one.
//
// Not used to decide passthrough -- anything unknown passes through, so ros2
// verbs added after this build still work.
var ros2Verbs = map[string]bool{
	"action": true, "bag": true, "component": true, "daemon": true,
	"doctor": true, "interface": true, "launch": true, "lifecycle": true,
	"multicast": true, "node": true, "param": true, "pkg": true,
	"plugin": true, "run": true, "security": true, "service": true,
	"topic": true, "wtf": true,
}

// Globals are ros2pi's own flags. They are a CLOSED set and may only appear
// BEFORE the verb.
//
// The rule, identical to git and docker: our flags go before the verb,
// everything after the verb belongs to ros2. So `ros2pi --verbose topic list`
// is ours-then-theirs, while `ros2pi topic list --verbose` sends --verbose to
// ros2. Without a rule this strict, `ros2pi run --help` becomes ambiguous.
type Globals struct {
	Workspace string
	Config    string
	Verbose   bool
	DryRun    bool
	NoTTY     bool
	Recreate  bool
	Root      bool
}

// Invocation is the parsed command line.
type Invocation struct {
	Mode     Mode
	Globals  Globals
	Verb     string
	OwnArgs  []string // args after our verb
	PassArgs []string // forwarded to ros2 VERBATIM
}

// Route parses argv. It is pure and never touches the filesystem.
func Route(argv []string) (Invocation, error) {
	var in Invocation

	i := 0
	for ; i < len(argv); i++ {
		a := argv[i]

		// Everything after `--` is passthrough, unconditionally. This is the
		// escape hatch for the day ros2 adds a verb that collides with ours.
		if a == "--" {
			in.Mode = ModePassthrough
			in.PassArgs = append([]string{}, argv[i+1:]...)
			if len(in.PassArgs) == 0 {
				return in, fmt.Errorf("`--` must be followed by a command to pass to ros2")
			}
			return in, nil
		}

		if !strings.HasPrefix(a, "-") {
			break // the verb
		}

		switch a {
		case "-h", "--help":
			in.Mode = ModeHelp
			return in, nil
		case "--version":
			in.Mode = ModeVersion
			return in, nil
		case "-v", "--verbose":
			in.Globals.Verbose = true
		case "--dry-run":
			in.Globals.DryRun = true
		case "--no-tty":
			in.Globals.NoTTY = true
		case "--recreate":
			in.Globals.Recreate = true
		case "--root":
			in.Globals.Root = true
		case "-C", "--workspace":
			v, next, err := value(argv, i, a)
			if err != nil {
				return in, err
			}
			in.Globals.Workspace, i = v, next
		case "--config":
			v, next, err := value(argv, i, a)
			if err != nil {
				return in, err
			}
			in.Globals.Config, i = v, next
		default:
			if k, v, ok := strings.Cut(a, "="); ok {
				switch k {
				case "-C", "--workspace":
					in.Globals.Workspace = v
					continue
				case "--config":
					in.Globals.Config = v
					continue
				}
			}
			return in, fmt.Errorf(
				"unknown ros2pi flag %q\n"+
					"  ros2pi's own flags go BEFORE the verb; everything after it goes to ros2.\n"+
					"  to pass %q to ros2, put it after the verb, or use: ros2pi -- <command>",
				a, a)
		}
	}

	if i >= len(argv) {
		in.Mode = ModeHelp // bare `ros2pi`
		return in, nil
	}

	verb := argv[i]
	rest := argv[i+1:]

	if _, ours := ownVerbs[verb]; ours {
		in.Mode, in.Verb = ModeOwn, verb
		// For OUR verbs, flags after the verb are unambiguous -- there is no
		// ros2 command to confuse them with -- so `ros2pi up --recreate` works,
		// as it must: that is the form users type, and the form our own error
		// messages recommend.
		//
		// The flags-before-verb rule exists only to disambiguate PASSTHROUGH,
		// where `ros2pi topic echo --once` has to send --once to ros2.
		args, err := parseOwnFlags(rest, &in.Globals)
		if err != nil {
			return in, fmt.Errorf("%w (in `ros2pi %s`)", err, verb)
		}
		in.OwnArgs = args
		return in, nil
	}

	in.Mode = ModePassthrough
	in.PassArgs = append([]string{verb}, rest...)
	return in, nil
}

// parseOwnFlags consumes ros2pi's flags from one of our verbs' arguments,
// returning what remains. Unknown flags are left alone: `ros2pi build
// --packages-select foo` must forward that to colcon.
func parseOwnFlags(args []string, g *Globals) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--":
			// Everything after is for the underlying command, verbatim.
			rest = append(rest, args[i+1:]...)
			return rest, nil
		case "-v", "--verbose":
			g.Verbose = true
		case "--dry-run":
			g.DryRun = true
		case "--no-tty":
			g.NoTTY = true
		case "--recreate":
			g.Recreate = true
		case "--root":
			g.Root = true
		case "-C", "--workspace":
			v, next, err := value(args, i, a)
			if err != nil {
				return nil, err
			}
			g.Workspace, i = v, next
		default:
			rest = append(rest, a)
		}
	}
	return rest, nil
}

func value(argv []string, i int, flag string) (string, int, error) {
	if i+1 >= len(argv) {
		return "", i, fmt.Errorf("%s needs a value", flag)
	}
	return argv[i+1], i + 1, nil
}

// OwnVerbs lists ros2pi's commands, sorted, for help output.
func OwnVerbs() []string {
	out := make([]string, 0, len(ownVerbs))
	for v := range ownVerbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// VerbHelp describes one of our verbs.
func VerbHelp(v string) string { return ownVerbs[v] }

// CollidesWithROS2 reports whether one of our verbs shadows a ros2 command.
// Nothing should; a test asserts it, so that adding a verb named `launch` fails
// the build rather than silently breaking passthrough for everyone.
func CollidesWithROS2() []string {
	var bad []string
	for v := range ownVerbs {
		if ros2Verbs[v] {
			bad = append(bad, v)
		}
	}
	sort.Strings(bad)
	return bad
}
