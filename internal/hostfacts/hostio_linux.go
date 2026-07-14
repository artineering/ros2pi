package hostfacts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// osHost implements HostIO against the real machine. It is the only type in the
// package that touches the OS.
type osHost struct {
	root fs.FS // os.DirFS("/")
}

// Compile-time proof that osHost satisfies the whole contract. This is the
// guarantee the interface buys: a method added to HostIO breaks the build here
// rather than surfacing as a nil dereference inside a probe.
var _ HostIO = osHost{}

// NewOSHost returns a HostIO backed by the real host.
func NewOSHost() HostIO { return osHost{root: os.DirFS("/")} }

// --- FileIO ---

func (h osHost) ReadFile(name string) ([]byte, error) { return fs.ReadFile(h.root, name) }

func (h osHost) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(h.root, name) }

func (h osHost) Readlink(path string) (string, error) { return os.Readlink(path) }

// --- DeviceIO ---

// StatDev follows symlinks deliberately: for /dev/serial0 the caller wants the
// major/minor of the real device, not of the link.
func (h osHost) StatDev(path string) (DevStat, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return DevStat{Exists: false}, nil
		}
		return DevStat{}, err
	}
	return DevStat{
		Exists:   true,
		IsChar:   st.Mode&unix.S_IFMT == unix.S_IFCHR,
		Major:    unix.Major(uint64(st.Rdev)),
		Minor:    unix.Minor(uint64(st.Rdev)),
		Mode:     st.Mode & 0o7777,
		OwnerGID: int(st.Gid),
	}, nil
}

// gpiochipInfo mirrors the kernel's struct gpiochip_info:
//
//	struct gpiochip_info { char name[32]; char label[32]; __u32 lines; };
//
// x/sys/unix exports GPIO_GET_CHIPINFO_IOCTL but no typed helper, so the ioctl
// is issued directly.
type gpiochipInfo struct {
	name  [32]byte
	label [32]byte
	lines uint32
}

func (h osHost) GPIOChipInfo(path string) (GPIOChipInfoResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return GPIOChipInfoResult{}, err
	}
	defer f.Close()

	var info gpiochipInfo
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		f.Fd(),
		uintptr(unix.GPIO_GET_CHIPINFO_IOCTL),
		uintptr(unsafe.Pointer(&info)),
	)
	if errno != 0 {
		return GPIOChipInfoResult{}, fmt.Errorf("ioctl GPIO_GET_CHIPINFO: %w", errno)
	}
	return GPIOChipInfoResult{
		Name:  nulString(info.name[:]),
		Label: nulString(info.label[:]),
		Lines: info.lines,
	}, nil
}

// --- IdentityIO ---

func (h osHost) Getenv(key string) string  { return os.Getenv(key) }
func (h osHost) Geteuid() int              { return os.Geteuid() }
func (h osHost) Getegid() int              { return os.Getegid() }
func (h osHost) Getgroups() ([]int, error) { return os.Getgroups() }

func (h osHost) LookupUID(uid int) (*user.User, error) {
	return user.LookupId(fmt.Sprint(uid))
}

func (h osHost) LookupGroup(name string) (*user.Group, error) { return user.LookupGroup(name) }

func (h osHost) GroupIDs(u *user.User) ([]string, error) { return u.GroupIds() }

// --- CommandIO ---

func (h osHost) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (h osHost) Exec(ctx context.Context, name string, args ...string) (RunResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	res := RunResult{Stdout: stdout.String(), Stderr: stderr.String()}

	var ee *exec.ExitError
	switch {
	case err == nil:
		res.Code = 0
	case errors.As(err, &ee):
		res.Code = ee.ExitCode() // a failed command is data, not an error
	default:
		return res, err // could not start the process at all
	}
	return res, nil
}

// --- PlatformIO ---

func (h osHost) Uname() (machine, release string, err error) {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "", "", err
	}
	return nulString(u.Machine[:]), nulString(u.Release[:]), nil
}

func (h osHost) GoArch() string { return runtime.GOARCH }

func nulString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
