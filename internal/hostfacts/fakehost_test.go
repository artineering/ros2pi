package hostfacts

import (
	"context"
	"io/fs"
	"os/user"
	"strconv"
	"testing/fstest"
)

// fakeHost implements HostIO from the plain data in a fakePi. The compiler
// checks it is complete, so a method added to HostIO fails the build here
// rather than panicking inside a probe at run time.
type fakeHost struct {
	p    fakePi
	fsys fstest.MapFS
}

var _ HostIO = fakeHost{}

func (p fakePi) host() HostIO {
	mfs := fstest.MapFS{}
	for name, body := range p.files {
		mfs[name] = &fstest.MapFile{Data: []byte(body)}
	}
	// /dev entries must exist as directory entries for enumeration to see them.
	for dev := range p.chips {
		mf := &fstest.MapFile{}
		if p.chipIsSym[baseName(dev)] {
			// fs.ModeSymlink is a FileMode bit, NOT the Unix octal 0o120000.
			mf.Mode |= fs.ModeSymlink
		}
		mfs["dev/"+baseName(dev)] = mf
	}
	for dev := range p.devs {
		mfs["dev/"+baseName(dev)] = &fstest.MapFile{}
	}
	for name, body := range p.extraFiles {
		mfs[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fakeHost{p: p, fsys: mfs}
}

// --- FileIO ---

func (h fakeHost) ReadFile(name string) ([]byte, error) { return fs.ReadFile(h.fsys, name) }

func (h fakeHost) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(h.fsys, name) }

func (h fakeHost) Readlink(path string) (string, error) {
	if t, ok := h.p.symlinks[path]; ok {
		return t, nil
	}
	// The real os.Readlink errors on a non-symlink; returning the path itself
	// is the fake's way of saying "not a link" and callers guard on it.
	return path, nil
}

// --- DeviceIO ---

func (h fakeHost) StatDev(path string) (DevStat, error) {
	if d, ok := h.p.devs[path]; ok {
		return d, nil
	}
	return DevStat{Exists: false}, nil
}

func (h fakeHost) GPIOChipInfo(path string) (GPIOChipInfoResult, error) {
	if i, ok := h.p.chips[path]; ok {
		return i, nil
	}
	return GPIOChipInfoResult{}, errNotFound
}

// --- IdentityIO ---

func (h fakeHost) Getenv(key string) string { return h.p.env[key] }
func (h fakeHost) Geteuid() int             { return 0 }
func (h fakeHost) Getegid() int             { return 0 }

func (h fakeHost) Getgroups() ([]int, error) {
	var out []int
	for name, gid := range h.p.groups {
		// A nil sessionGroup means the session carries everything the database
		// says; a map lets a test model the stale-session gap.
		if h.p.sessionGroup != nil && !h.p.sessionGroup[name] {
			continue
		}
		if n, err := strconv.Atoi(gid); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func (h fakeHost) LookupUID(int) (*user.User, error) {
	return &user.User{Uid: "1000", Gid: "1000", Username: "pi"}, nil
}

func (h fakeHost) LookupGroup(name string) (*user.Group, error) {
	if gid, ok := h.p.groups[name]; ok {
		return &user.Group{Name: name, Gid: gid}, nil
	}
	return nil, errNotFound
}

func (h fakeHost) GroupIDs(*user.User) ([]string, error) {
	gids := make([]string, 0, len(h.p.groups))
	for _, gid := range h.p.groups {
		gids = append(gids, gid)
	}
	return gids, nil
}

// --- CommandIO ---

func (h fakeHost) LookPath(file string) (string, error) {
	if file == "docker" && h.p.noDocker {
		return "", errNotFound
	}
	return "/usr/bin/" + file, nil
}

func (h fakeHost) Exec(_ context.Context, name string, args ...string) (RunResult, error) {
	switch {
	case name == "dpkg":
		return RunResult{Stdout: h.p.dpkg}, nil
	case name == "docker" && len(args) > 0 && args[0] == "version":
		return RunResult{Stdout: "29.6.1\n"}, nil
	case name == "docker":
		return RunResult{Stdout: h.p.dockerOut, Stderr: h.p.dockerErr, Code: h.p.dockerCode}, nil
	}
	return RunResult{}, nil
}

// --- PlatformIO ---

func (h fakeHost) Uname() (string, string, error) { return h.p.machine, h.p.release, nil }
func (h fakeHost) GoArch() string                 { return "arm64" }

// override replaces selected methods of a HostIO, delegating the rest to the
// embedded value.
//
// This is what preserves the one-line-stub ergonomics that the struct-of-funcs
// seam had, WITHOUT reintroducing its hazard: the embedded HostIO is a real,
// complete host, so an un-overridden method calls a working implementation
// rather than dereferencing nil.
type override struct {
	HostIO
	readlink  func(string) (string, error)
	statDev   func(string) (DevStat, error)
	lookupUID func(int) (*user.User, error)
}

func (o override) Readlink(p string) (string, error) {
	if o.readlink != nil {
		return o.readlink(p)
	}
	return o.HostIO.Readlink(p)
}

func (o override) StatDev(p string) (DevStat, error) {
	if o.statDev != nil {
		return o.statDev(p)
	}
	return o.HostIO.StatDev(p)
}

func (o override) LookupUID(uid int) (*user.User, error) {
	if o.lookupUID != nil {
		return o.lookupUID(uid)
	}
	return o.HostIO.LookupUID(uid)
}

// probe runs the prober against a fake host.
func probe(p fakePi) (HostFacts, error) {
	return NewLinuxProber(p.host(), "test").Probe(context.Background())
}
