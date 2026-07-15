// Package distro knows which ROS 2 distributions exist and what to say about
// them.
//
// The list is built in rather than fetched, so `ros2pi init` needs no network
// on a Pi that may not have one. It WILL go stale -- ROS releases every May --
// and that is handled by never treating the list as exhaustive: an unknown
// distro is accepted with a note, not refused. Being out of date must mean "not
// offered", never "blocked".
//
// That is not a hypothetical. This list was first written from memory and got
// it wrong twice: it offered Iron, which has been end-of-life since 2024, and
// omitted Lyrical entirely. Both were caught by reading ros/rosdistro instead
// of trusting the author. Anything here can be checked the same way:
//
//	curl -s https://raw.githubusercontent.com/ros/rosdistro/master/index-v4.yaml
package distro

import "fmt"

// Support says how much a distro can be relied on.
type Support string

const (
	// LTS: five years of support. What a robot that has to keep working wants.
	LTS Support = "lts"
	// Regular: roughly 18 months.
	Regular Support = "regular"
	// Development: the rolling branch. Changes without warning.
	Development Support = "development"
)

// Distro is one ROS 2 distribution.
type Distro struct {
	Name    string
	Support Support
	// EOL is the year support ends, for the picker. Empty for rolling.
	EOL string
	// Note is the one line a user needs to choose.
	Note string
}

// Default is what ros2pi picks when nobody says otherwise: the LTS with the
// longest remaining support.
const Default = "jazzy"

// known is every distro this build has been told about, in the order the picker
// offers them: safest first.
//
// Verified against ros/rosdistro index-v4.yaml (distribution_status: active)
// and against `docker manifest inspect ros:<name>` for a native arm64 build, on
// 2026-07-15. End-of-life distros are deliberately absent: offering Iron, which
// died in 2024, would be worse than not listing it.
var known = []Distro{
	{
		Name: "jazzy", Support: LTS, EOL: "2029",
		Note: "long-term support, the safe choice",
	},
	{
		Name: "humble", Support: LTS, EOL: "2027",
		Note: "older long-term support; pick this to match existing code or tutorials",
	},
	{
		Name: "kilted", Support: Regular, EOL: "2026",
		Note: "shorter support window",
	},
	{
		Name: "lyrical", Support: Regular, EOL: "2027",
		Note: "newest release",
	},
	{
		Name: "rolling", Support: Development,
		Note: "development branch; changes without warning, not for a robot you rely on",
	},
}

// All returns the distros this build knows, in picker order.
func All() []Distro { return append([]Distro{}, known...) }

// Lookup finds a distro by name. The bool is false for anything not in the
// built-in list -- which is not an error, only a fact the caller should mention.
func Lookup(name string) (Distro, bool) {
	for _, d := range known {
		if d.Name == name {
			return d, true
		}
	}
	return Distro{Name: name}, false
}

// Image is the official image for a distro.
func Image(name string) string { return "ros:" + name }

// Describe is the one-line summary shown in the picker.
func (d Distro) Describe() string {
	switch d.Support {
	case LTS:
		return fmt.Sprintf("LTS, supported to %s -- %s", d.EOL, d.Note)
	case Development:
		return d.Note
	default:
		return fmt.Sprintf("supported to %s -- %s", d.EOL, d.Note)
	}
}

// UnknownNote is what to tell someone who asked for a distro this build has not
// heard of. It is deliberately not a warning about THEM: a new ROS release is
// expected, and this build simply predates it.
func UnknownNote(name string) string {
	return fmt.Sprintf(
		"%q is not a distribution this ros2pi knows about -- it may be newer than this build.\n"+
			"  Using it anyway. ros2pi will check it against the image when the container starts,\n"+
			"  so a typo will be caught then rather than silently ignored.", name)
}
