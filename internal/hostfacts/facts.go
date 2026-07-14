// Package hostfacts probes the Raspberry Pi host for everything ros2pi needs to
// know before it can construct a docker invocation.
//
// This package is the ONLY place that touches the host. Everything downstream
// (dockerargs, check) consumes a HostFacts value and must stay pure, so that
// Pi 4 / Pi 5 / kernel-variant behaviour is testable without that hardware.
package hostfacts

import (
	"strconv"
	"time"
)

// SchemaVersion is bumped whenever HostFacts changes shape, so that fixtures
// captured by older builds can be detected rather than silently misread.
const SchemaVersion = 1

// HostFacts is a complete, serialisable snapshot of the host. It round-trips
// through JSON: `ros2pi check --dump-facts` emits one, and a bug report that
// includes it becomes a regression fixture.
type HostFacts struct {
	SchemaVersion int       `json:"schema_version"`
	ProbedAt      time.Time `json:"probed_at"`
	ProbedBy      string    `json:"probed_by"` // ros2pi version

	Arch     Arch      `json:"arch"`
	Kernel   Kernel    `json:"kernel"`
	OS       OSRelease `json:"os"`
	Model    PiModel   `json:"model"`
	Identity Identity  `json:"identity"`

	// Groups is a slice sorted by Name, deliberately NOT a map. Downstream
	// argv synthesis must be byte-deterministic so that a plan fingerprint is
	// stable; ranging over a map would randomise --group-add ordering between
	// runs, flap the fingerprint, and make a running container intermittently
	// look stale. Ordering is a correctness property here, so it is enforced by
	// the type rather than by convention. Look up with Group().
	Groups []Group `json:"groups"`

	GPIOChips []GPIOChip `json:"gpio_chips"`
	GPIOMem   []DevNode  `json:"gpio_mem"`
	I2CBuses  []DevNode  `json:"i2c_buses"`
	I2CHeader I2CHeader  `json:"i2c_header"`
	SPIDevs   []DevNode  `json:"spi_devs"`
	Serial    []DevNode  `json:"serial"`
	USBSerial []DevNode  `json:"usb_serial"`

	Boot   Boot        `json:"boot"`
	Docker DockerFacts `json:"docker"`

	Warnings []Warning `json:"warnings,omitempty"`
}

// DockerProblem classifies why Docker is unusable. Docker reports several
// genuinely different failures with a similar shape, and they need opposite
// fixes -- so the classification, not the raw message, is what downstream code
// branches on.
type DockerProblem string

const (
	DockerOK DockerProblem = "ok"
	// DockerAbsent: no docker binary on PATH.
	DockerAbsent DockerProblem = "binary_absent"
	// DockerDaemonUnreachable: binary works, daemon is not running.
	DockerDaemonUnreachable DockerProblem = "daemon_unreachable"
	// DockerPermissionDenied: the user is genuinely not in the docker group.
	DockerPermissionDenied DockerProblem = "permission_denied"
	// DockerPermissionStaleSession: the user IS in the docker group per
	// /etc/group, but this process's credentials predate that change. Same
	// error text as DockerPermissionDenied, completely different fix: log in
	// again (or `newgrp docker`), do NOT run usermod again, and do NOT use sudo.
	DockerPermissionStaleSession DockerProblem = "permission_denied_stale_session"
	// DockerUnknownFailure: docker ran and failed in a way we do not recognise.
	// Detail carries the raw stderr so a bug report can teach us the case.
	DockerUnknownFailure DockerProblem = "unknown_failure"
)

// DockerFacts describes the one dependency ros2pi has.
type DockerFacts struct {
	Binary  string        `json:"binary"` // resolved path; "" when absent
	Problem DockerProblem `json:"problem"`
	Detail  string        `json:"detail,omitempty"` // raw stderr when classification failed

	ClientVersion string `json:"client_version,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	ServerArch    string `json:"server_arch,omitempty"`

	// CgroupVersion and Rootless gate hardware support: rootless Docker has no
	// device-cgroup control at all, so --device-cgroup-rule hotplug silently
	// fails at open() rather than at create.
	CgroupVersion string `json:"cgroup_version,omitempty"`
	Rootless      bool   `json:"rootless"`
}

// Usable reports whether docker can actually be driven.
func (d DockerFacts) Usable() bool { return d.Problem == DockerOK }

// Arch records all three architecture views. They can disagree: the classic Pi
// trap is a 64-bit kernel with a 32-bit userland, where Machine is aarch64 but
// Dpkg is armhf. Any disagreement is fatal and must be reported, not averaged.
type Arch struct {
	Machine string `json:"machine"` // uname -m
	Dpkg    string `json:"dpkg"`    // dpkg --print-architecture
	Go      string `json:"go"`      // runtime.GOARCH of this binary
}

type Kernel struct {
	Release string `json:"release"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Patch   int    `json:"patch"`
}

type OSRelease struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	Pretty    string `json:"pretty"`
}

// Family is derived from the GPIO chip label, never from the model string.
// The model string is for humans; the label is what determines device layout.
type Family string

const (
	FamilyPi4     Family = "pi4"     // pinctrl-bcm2711
	FamilyPi5     Family = "pi5"     // pinctrl-rp1
	FamilyPi123   Family = "pi123"   // pinctrl-bcm2835 / bcm2711 predecessors
	FamilyUnknown Family = "unknown" // fail loud; never guess
)

type PiModel struct {
	Raw    string `json:"raw"` // /proc/device-tree/model, NUL-trimmed
	Family Family `json:"family"`
}

type GPIOChip struct {
	Name   string `json:"name"`   // gpiochip0
	Label  string `json:"label"`  // pinctrl-bcm2711
	Dev    string `json:"dev"`    // /dev/gpiochip0
	Lines  uint32 `json:"lines"`  // 58
	Header bool   `json:"header"` // true if this is the 40-pin header chip
	Source string `json:"source"` // "ioctl" | "sysfs" — provenance matters for bugs
}

// DevNode carries major/minor because device-cgroup rules are written in terms
// of major numbers (c 188:* rmw), and Resolved because /dev/serial0 is a
// symlink that docker --device handles badly.
type DevNode struct {
	Path     string `json:"path"`
	Resolved string `json:"resolved,omitempty"`
	Exists   bool   `json:"exists"`
	Major    uint32 `json:"major"`
	Minor    uint32 `json:"minor"`
	Mode     uint32 `json:"mode"`
	OwnerGID int    `json:"owner_gid"`
}

// Group records a host group.
//
// NameResolvable means the host's GID for this group is the same value the ROS
// image will resolve that name to, so `--group-add <name>` is safe. It is
// COMPUTED by comparing the host GID against the known Debian-stable GID, never
// asserted from the name alone: if an admin has moved dialout off 20, passing
// it by name grants the container GID 20 while the device is owned by the host
// GID, and open() fails with EPERM.
//
// The Pi's gpio/i2c/spi groups are allocated dynamically by raspberrypi-sys-mods
// and do not exist in the ROS image at all, so they are never NameResolvable and
// MUST be passed as numeric GIDs.
type Group struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`

	// Exists reports whether the group is present on the host. When false, GID
	// is meaningless and must not be used.
	Exists bool `json:"exists"`

	NameResolvable bool `json:"name_resolvable"`

	// UserIsMember reflects the group DATABASE (/etc/group).
	UserIsMember bool `json:"user_is_member"`

	// SessionHasGroup reflects THIS PROCESS's credentials (getgroups(2)).
	//
	// It can lag UserIsMember: `usermod -aG docker $USER` updates the database
	// immediately, but a running shell keeps the credentials it started with
	// until the user logs in again. That gap produces a permission error that
	// looks exactly like "you were never added", while needing the opposite
	// fix, so the two are recorded separately rather than conflated.
	SessionHasGroup bool `json:"session_has_group"`
}

// MembershipIsStale reports that the user was added to this group but the
// current session predates the change, so the group is not yet in effect.
func (g Group) MembershipIsStale() bool {
	return g.Exists && g.UserIsMember && !g.SessionHasGroup
}

// GroupAddArg returns the value to pass to `docker --group-add` for this group,
// preferring the name only when it provably resolves to the same GID inside the
// container. Returns false when the group is unusable.
func (g Group) GroupAddArg() (string, bool) {
	if !g.Exists {
		return "", false
	}
	if g.NameResolvable {
		return g.Name, true
	}
	return strconv.Itoa(g.GID), true
}

type Identity struct {
	UID       int    `json:"uid"`
	GID       int    `json:"gid"`
	Username  string `json:"username"`
	UnderSudo bool   `json:"under_sudo"` // SUDO_UID won over geteuid
}

// Tristate distinguishes "explicitly off" from "never mentioned", which matters
// for config.txt: a commented-out dtparam is Unset, not Off.
type Tristate string

const (
	Unset Tristate = "unset"
	On    Tristate = "on"
	Off   Tristate = "off"
)

// Boot describes the firmware config. ConfigPath is resolved at probe time
// because /boot/config.txt is a stub on modern Pi OS and the real file lives at
// /boot/firmware/config.txt; editing the wrong one silently does nothing.
type Boot struct {
	ConfigPath string   `json:"config_path"`
	Found      bool     `json:"found"`
	StubSeen   bool     `json:"stub_seen"`
	I2CArm     Tristate `json:"dtparam_i2c_arm"`
	SPI        Tristate `json:"dtparam_spi"`
	EnableUART Tristate `json:"enable_uart"`
}

type Warning struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// I2CHeaderSource records how the header bus was identified. Provenance is
// reported so a bug report distinguishes "we detected this" from "we guessed".
type I2CHeaderSource string

const (
	// I2CFromAlias means the bus was resolved through a device-tree alias to
	// the controller node -- authoritative.
	I2CFromAlias I2CHeaderSource = "alias"
	// I2CAssumed means no alias resolved and the conventional bus number was
	// assumed. Always accompanied by a Warning.
	I2CAssumed I2CHeaderSource = "assumed"
	// I2CUnknown means the header bus could not be determined at all.
	I2CUnknown I2CHeaderSource = "unknown"
)

// i2cAssumedPath is the conventional header bus, used ONLY as a last resort
// when no device-tree alias resolves, and always with a Warning attached.
const i2cAssumedPath = "/dev/i2c-1"

// I2CHeader identifies the I2C bus wired to header pins 3 and 5 -- the only bus
// a user means by "i2c".
//
// The bus NUMBER is not hardcoded, for the same reason GPIO chip numbers are
// not: it is a kernel-assigned artefact, not a stable property of the hardware.
// It is resolved through the device-tree alias (i2c_arm, else i2c1) to the
// controller node, then matched against /sys/bus/i2c/devices/*/of_node.
type I2CHeader struct {
	Path   string          `json:"path"`   // e.g. /dev/i2c-1; "" if unknown
	Source I2CHeaderSource `json:"source"` // how Path was arrived at
	Alias  string          `json:"alias"`  // DT node the alias pointed to
}

// HeaderChip returns the GPIO chip driving the 40-pin header. The bool is false
// when no chip carried a recognised label; callers must handle that by warning,
// never by assuming gpiochip0 -- that assumption is exactly what broke on Pi 5.
//
// Returns by value: HostFacts is the immutable input to the pure argv core, and
// handing out a pointer into its backing array would let one consumer mutate
// what every other copy sees.
func (f HostFacts) HeaderChip() (GPIOChip, bool) {
	for _, c := range f.GPIOChips {
		if c.Header {
			return c, true
		}
	}
	return GPIOChip{}, false
}

// I2CHeaderBus returns the 40-pin header's I2C bus node, if it is present.
//
// The presence of *some* /dev/i2c-N proves nothing: a Pi 4 with i2c_arm unset
// still exposes i2c-0/10/20/21/22 for HDMI DDC and camera/display buses. Only
// the header bus answers "can I talk to my sensor?", so callers must ask for it
// via this accessor rather than globbing.
func (f HostFacts) I2CHeaderBus() (DevNode, bool) {
	if f.I2CHeader.Path == "" {
		return DevNode{}, false
	}
	for _, b := range f.I2CBuses {
		if b.Path == f.I2CHeader.Path && b.Exists {
			return b, true
		}
	}
	return DevNode{}, false
}

// Group looks up a probed host group by name.
func (f HostFacts) Group(name string) (Group, bool) {
	for _, g := range f.Groups {
		if g.Name == name {
			return g, true
		}
	}
	return Group{}, false
}

// familyForLabel maps a GPIO chip label to a Pi family. Unknown labels return
// FamilyUnknown and false so the caller can warn and pass through rather than
// guess a device layout.
func familyForLabel(label string) (Family, bool) {
	switch label {
	case "pinctrl-rp1":
		return FamilyPi5, true
	case "pinctrl-bcm2711":
		return FamilyPi4, true
	case "pinctrl-bcm2835", "pinctrl-bcm2836", "pinctrl-bcm2837":
		return FamilyPi123, true
	}
	return FamilyUnknown, false
}
