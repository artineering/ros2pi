package cli

import "strings"

// destFlag is the only way to choose where `ros2 pkg create` writes. Verified
// against ros2 pkg create --help on jazzy: there is no short form, so detecting
// this one string is sufficient.
const destFlag = "--destination-directory"

// PkgSrcDir is where ROS 2 workspaces keep source packages, by convention that
// colcon and every tutorial assume.
const PkgSrcDir = "src"

// adjustPassthrough is the ONE place ros2pi modifies a command before handing
// it to ros2. It returns the arguments to run and a note to show the user.
//
// The rule everywhere else is that passthrough arguments are forwarded
// verbatim, and that rule is load-bearing: the moment ros2pi starts parsing
// ros2's command lines, it inherits the job of tracking every flag ros2 ever
// adds. This is a deliberate exception, kept as narrow as possible.
//
// The exception exists because the mistake it prevents is silent. `ros2 pkg
// create foo` run from a workspace root creates ./foo instead of ./src/foo, and
// then colcon builds it anyway -- it searches recursively -- so nothing ever
// tells you the layout is wrong. A mistake you cannot notice is worth more than
// consistency here.
//
// It fires only when all of these hold:
//   - the command is exactly `pkg create`
//   - the user did not choose a destination themselves
//   - it is not a help request
//
// So `ros2pi pkg create --destination-directory . foo` still does exactly what
// it says, and anyone who knows ros2 can always override us.
func adjustPassthrough(args []string) (out []string, note string) {
	if !isPkgCreate(args) || hasDest(args) || wantsHelp(args) {
		return args, ""
	}
	out = append(append([]string{}, args...), destFlag, PkgSrcDir)
	return out, "creating the package in " + PkgSrcDir + "/ (ros2 would use the " +
		"workspace root; pass " + destFlag + " to choose)"
}

func isPkgCreate(args []string) bool {
	return len(args) >= 2 && args[0] == "pkg" && args[1] == "create"
}

func hasDest(args []string) bool {
	for _, a := range args {
		// Both spellings: `--destination-directory src` and `--dest...=src`.
		if a == destFlag || strings.HasPrefix(a, destFlag+"=") {
			return true
		}
	}
	return false
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}
