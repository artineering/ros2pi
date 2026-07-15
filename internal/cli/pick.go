package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/artineering/ros2pi/internal/distro"
)

// pickDistro decides which ROS 2 distribution a new workspace targets.
//
// Precedence: an explicit --distro always wins; otherwise ask, if there is
// somebody to ask; otherwise fall back to the default and SAY SO. A script
// piping into ros2pi must never hang on a prompt nobody can see, and must never
// silently get a choice it did not make.
func (a App) pickDistro(flag string, in io.Reader, interactive bool) (string, error) {
	if flag != "" {
		if _, known := distro.Lookup(flag); !known {
			// Not an error: ROS releases every May, and this build may simply
			// predate what the user is asking for.
			fmt.Fprintln(a.Stderr, "ros2pi: "+distro.UnknownNote(flag))
		}
		return flag, nil
	}

	if !interactive {
		d, _ := distro.Lookup(distro.Default)
		fmt.Fprintf(a.Stderr,
			"ros2pi: no terminal to ask, so using %s (%s).\n"+
				"        override with --distro, or edit ros2pi.toml\n",
			d.Name, d.Describe())
		return distro.Default, nil
	}
	return a.promptDistro(in)
}

func (a App) promptDistro(r io.Reader) (string, error) {
	all := distro.All()

	fmt.Fprintf(a.Stdout, "Which ROS 2 distribution?\n\n")
	for i, d := range all {
		marker := " "
		if d.Name == distro.Default {
			marker = "*"
		}
		fmt.Fprintf(a.Stdout, "  %s %d) %-8s %s\n", marker, i+1, d.Name, d.Describe())
	}
	fmt.Fprintf(a.Stdout, "\n  * default. Press enter to take it, or type a number or a name.\n")
	fmt.Fprintf(a.Stdout, "\n  If you are unsure: %s. It is supported the longest, and you can\n",
		distro.Default)
	fmt.Fprintf(a.Stdout, "  change it later by editing ros2pi.toml.\n")

	br := bufio.NewReader(r)
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(a.Stdout, "\ndistro [%s]: ", distro.Default)

		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			// stdin closed mid-prompt (^D, or a pipe that ended): take the
			// default rather than looping on an empty reader forever.
			fmt.Fprintln(a.Stdout)
			return distro.Default, nil
		}

		switch choice := strings.TrimSpace(line); {
		case choice == "":
			return distro.Default, nil

		case isNumber(choice):
			n, _ := strconv.Atoi(choice)
			if n >= 1 && n <= len(all) {
				return all[n-1].Name, nil
			}
			fmt.Fprintf(a.Stderr, "  there is no option %d\n", n)

		default:
			if _, known := distro.Lookup(choice); known {
				return choice, nil
			}
			// A name we do not know is allowed -- but at an interactive prompt
			// it is far more likely a typo than a distro from the future, so
			// confirm rather than accept silently.
			fmt.Fprintf(a.Stderr, "  %q is not one of the options above.\n", choice)
			fmt.Fprintf(a.Stderr, "  If you meant a newer ROS release, pass it explicitly:\n")
			fmt.Fprintf(a.Stderr, "    ros2pi init --distro %s\n", choice)
		}
	}
	return "", fmt.Errorf("no distribution chosen after 3 attempts")
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
