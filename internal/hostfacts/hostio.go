package hostfacts

import (
	"context"
	"io/fs"
	"os/user"
	"strings"
)

// HostIO is the single seam between hostfacts and the outside world. Everything
// the LinuxProber touches goes through it, so tests drive a fake host and no
// probe reaches the real machine.
//
// It is an interface rather than a struct of function fields for one concrete
// reason: a type either implements every method or the build fails. The struct
// form could be partially populated, and a missing field surfaced as a nil
// dereference deep inside a probe -- which is exactly what happened when
// Getgroups/LookPath/Exec were added. That failure mode is now a compile error.
//
// It is composed from five role interfaces rather than declared flat so each
// probe can ask for the narrow slice it actually uses (see gpioIO, dockerIO),
// and so the seam reads as five responsibilities instead of a bag of methods.
//
// PATH CONVENTIONS DIFFER, deliberately and unavoidably:
//   - FileIO uses fs.FS paths: slash-separated, rooted at /, NO leading slash
//     ("etc/os-release").
//   - Readlink, StatDev and GPIOChipInfo take ABSOLUTE OS paths ("/dev/serial0"),
//     because they report values that end up in HostFacts and in docker flags.
//
// The split exists because fs.FS models neither symlinks, nor device
// major/minor, nor ioctls -- and all three are load-bearing here.
type HostIO interface {
	FileIO
	DeviceIO
	IdentityIO
	CommandIO
	PlatformIO
}

// FileIO reads the host filesystem. Paths are fs.FS-style: rooted at /, with no
// leading slash. Readlink is the exception (see below).
type FileIO interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]fs.DirEntry, error)

	// Readlink takes an ABSOLUTE path and returns the link target VERBATIM,
	// which for /dev/serial0 is the relative "ttyS0". Callers must resolve it
	// themselves; fs.FS cannot model symlinks at all.
	Readlink(path string) (string, error)
}

// DeviceIO inspects device nodes. Both methods take absolute paths.
type DeviceIO interface {
	// StatDev reports device major/minor, which device-cgroup rules are written
	// in terms of ("c 188:* rmw"). A missing node is (DevStat{Exists:false}, nil),
	// not an error.
	StatDev(path string) (DevStat, error)

	// GPIOChipInfo issues the GPIO chardev ioctl. This is the only supported way
	// to read a chip's label; the sysfs interface that also exposes it is
	// deprecated.
	GPIOChipInfo(path string) (GPIOChipInfoResult, error)
}

// IdentityIO answers who the user is.
type IdentityIO interface {
	Getenv(key string) string
	Geteuid() int
	Getegid() int

	// Getgroups reports the supplementary groups of THIS PROCESS
	// (getgroups(2)), which can lag GroupIDs: `usermod -aG docker $USER` updates
	// the database at once, but a running shell keeps its original credentials
	// until the user logs in again. Comparing the two is what distinguishes
	// "you are not in the docker group" from "you are, but this shell predates
	// it" -- identical symptoms, opposite fixes.
	Getgroups() ([]int, error)

	LookupUID(uid int) (*user.User, error)
	LookupGroup(name string) (*user.Group, error)

	// GroupIDs reports the user's groups per the DATABASE (/etc/group).
	GroupIDs(u *user.User) ([]string, error)
}

// CommandIO runs external programs. In practice that means docker and dpkg.
type CommandIO interface {
	// LookPath reports a binary's absence distinctly from a binary that runs
	// and fails -- "docker is not installed" and "docker cannot reach its
	// daemon" need different advice.
	LookPath(file string) (string, error)

	// Exec runs a command to completion. A non-zero exit is NOT an error: it is
	// data. Docker reports every interesting failure with exit 1 and a distinct
	// stderr, so the error is returned only when the process could not start.
	Exec(ctx context.Context, name string, args ...string) (RunResult, error)
}

// PlatformIO reports properties of the running system and binary.
type PlatformIO interface {
	Uname() (machine, release string, err error)

	// GoArch is the architecture of THIS BINARY, one of the three views that
	// must agree (kernel, dpkg userland, binary).
	GoArch() string
}

// RunResult is the outcome of a command that ran. A non-zero Code is data to be
// classified, not a failure to be propagated.
type RunResult struct {
	Stdout string
	Stderr string
	Code   int
}

// DevStat is the subset of stat(2) that matters for device passthrough.
type DevStat struct {
	Exists   bool
	IsChar   bool
	Major    uint32
	Minor    uint32
	Mode     uint32
	OwnerGID int
}

// GPIOChipInfoResult mirrors struct gpiochip_info from the kernel's GPIO
// chardev ABI.
type GPIOChipInfoResult struct {
	Name  string
	Label string
	Lines uint32
}

// gpioIO is the slice of the host that GPIO probing needs. Narrow interfaces
// like this are the payoff of composing HostIO from roles: the signature states
// that GPIO probing reads files and devices and touches nothing else.
type gpioIO interface {
	FileIO
	DeviceIO
}

// dockerIO is the slice of the host that docker probing needs.
type dockerIO interface {
	CommandIO
}

// readTrimmed reads a whole file and trims surrounding whitespace.
func readTrimmed(io FileIO, name string) (string, error) {
	b, err := io.ReadFile(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
