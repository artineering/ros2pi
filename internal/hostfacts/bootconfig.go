package hostfacts

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// config.txt is not a flat key=value file. It supports conditional filter
// sections: every setting after a filter applies only when that filter matches,
// until the next filter. A parser that ignores them reports settings meant for
// other hardware as active on this one.
//
// This is the common case, not a corner case: the stock Raspberry Pi OS
// config.txt ships with [cm4], [cm5], [pi5] and [all] sections out of the box.
//
// Reference: https://www.raspberrypi.com/documentation/computers/config_txt.html
var sectionRE = regexp.MustCompile(`^\s*\[([^\]]*)\]\s*$`)

var dtparamRE = regexp.MustCompile(`^\s*(dtparam|enable_uart)\s*=\s*(.+?)\s*$`)

// filterVerdict is the result of evaluating a [section] header against the host.
type filterVerdict int

const (
	filterApplies filterVerdict = iota
	filterExcludes
	// filterUnknown covers conditions we cannot evaluate from HostFacts alone
	// (e.g. [EDID=...], [gpio4=1], serial-number filters). Settings in such a
	// section are NOT applied, and a warning records the uncertainty rather
	// than silently guessing either way.
	filterUnknown
)

// modelFilters maps a config.txt filter to the Pi families it selects.
//
// Raspberry Pi's model filters are SoC-based, not board-based: [pi4] matches any
// BCM2711 board, which includes the Pi 400 and CM4, not just the Pi 4 B.
var modelFilters = map[string][]Family{
	"pi5":   {FamilyPi5},
	"pi4":   {FamilyPi4},
	"pi400": {FamilyPi4},
	"pi3":   {FamilyPi123},
	"pi2":   {FamilyPi123},
	"pi1":   {FamilyPi123},
	"pi0":   {FamilyPi123},
	"pi02":  {FamilyPi123},
}

// computeModuleFilters select Compute Modules specifically, which the family
// alone cannot distinguish (a CM4 is BCM2711, same as a Pi 4 B).
var computeModuleFilters = map[string]bool{
	"cm1": true, "cm3": true, "cm4": true, "cm4s": true, "cm5": true,
}

// evalFilter decides whether a [section] applies to this host.
func evalFilter(name string, m PiModel) filterVerdict {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "all":
		return filterApplies
	case "none":
		// [none] is the documented way to disable a block entirely.
		return filterExcludes
	}

	key := strings.ToLower(strings.TrimSpace(name))

	if fams, ok := modelFilters[key]; ok {
		if m.Family == FamilyUnknown {
			return filterUnknown // cannot judge without knowing the family
		}
		for _, f := range fams {
			if f == m.Family {
				return filterApplies
			}
		}
		return filterExcludes
	}

	if computeModuleFilters[key] {
		if m.Raw == "" {
			return filterUnknown
		}
		if strings.Contains(strings.ToLower(m.Raw), "compute module") {
			// Right class of board, but we do not pin the exact CM revision;
			// say so rather than claim a match.
			return filterUnknown
		}
		return filterExcludes
	}

	// [EDID=...], [gpio4=1], [0x<serial>], [board-type=...] and friends.
	return filterUnknown
}

// parseBootConfig extracts the dtparams that apply to THIS host, honouring
// conditional filter sections.
//
// Settings before any filter are unconditional. A filter is in force until the
// next filter; [all] returns to unconditional.
func parseBootConfig(data string, m PiModel, b *Boot) []Warning {
	var warns []Warning
	seenUnknown := map[string]bool{}

	verdict := filterApplies // pre-filter lines are unconditional
	section := ""

	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := sc.Text()

		// A section header must be examined before comment-stripping, but '#'
		// cannot appear inside a filter name, so order is not load-bearing.
		if mm := sectionRE.FindStringSubmatch(line); mm != nil {
			section = mm[1]
			verdict = evalFilter(section, m)
			if verdict == filterUnknown && !seenUnknown[section] {
				seenUnknown[section] = true
				warns = append(warns, Warning{
					Code: "boot.config.filter.unevaluated",
					Detail: fmt.Sprintf(
						"config.txt section [%s] could not be evaluated for this host; "+
							"settings inside it were ignored", section),
				})
			}
			continue
		}

		if verdict != filterApplies {
			continue // belongs to other hardware, or we cannot tell
		}

		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i] // a commented dtparam is not a set dtparam
		}
		applyConfigLine(line, b)
	}
	return warns
}

func applyConfigLine(line string, b *Boot) {
	m := dtparamRE.FindStringSubmatch(line)
	if m == nil {
		return
	}
	if m[1] == "enable_uart" {
		b.EnableUART = tristate(m[2])
		return
	}
	// A single dtparam= line may carry several comma-separated params.
	for _, kv := range strings.Split(m[2], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "i2c_arm", "i2c1":
			b.I2CArm = tristate(v)
		case "spi":
			b.SPI = tristate(v)
		}
	}
}

func tristate(v string) Tristate {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "on", "true", "1", "yes":
		return On
	case "off", "false", "0", "no":
		return Off
	}
	return Unset
}
