// Package imagefacts asks docker about one ROS image.
//
// It is separate from hostfacts because it answers a different kind of
// question. hostfacts describes the Pi, and knows nothing about any workspace.
// "Is ros:jazzy pulled, and is it the right architecture?" is a question about
// a specific workspace's config, so it is probed separately and handed to the
// pure checks alongside the host facts.
package imagefacts

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Problem says why an image cannot be used, if it cannot.
type Problem string

const (
	OK Problem = "ok"
	// Absent: not pulled. Not fatal -- docker will fetch it -- but worth
	// warning about, because the first command then stalls on a 1.3 GB
	// download with no explanation.
	Absent Problem = "absent"
	// WrongArch: the image cannot run on this host. This is the one that
	// produces a genuinely baffling failure, so it is worth catching early.
	WrongArch Problem = "wrong_arch"
	// Unknown: docker failed in a way we do not recognise.
	Unknown Problem = "unknown"
)

// Facts is what docker knows about an image.
type Facts struct {
	// Ref is what was asked for, e.g. "ros:jazzy".
	Ref     string  `json:"ref"`
	Problem Problem `json:"problem"`

	Present bool   `json:"present"`
	Arch    string `json:"arch,omitempty"` // arm64, amd64
	OS      string `json:"os,omitempty"`

	// Size is docker's own human-readable on-disk size, e.g. "1.33GB".
	//
	// Deliberately the string from `docker images` rather than inspect's .Size
	// field: under Docker's containerd image store those disagree badly --
	// inspect reports 287MB for an image `docker images` calls 1.33GB, because
	// it is measuring the compressed form. The number a user can check
	// themselves is the useful one.
	Size string `json:"size,omitempty"`

	Digest  string    `json:"digest,omitempty"`
	Created time.Time `json:"created,omitempty"`

	// ROSDistro is what the IMAGE says it contains, read from its ROS_DISTRO
	// environment variable. That is what makes a config/image mismatch
	// detectable before anything runs, rather than at the moment a node fails.
	ROSDistro string `json:"ros_distro,omitempty"`

	Detail string `json:"detail,omitempty"` // raw stderr when Problem is Unknown
}

// Runner is the slice of the world this package needs. It matches
// hostfacts.CommandIO, so a caller can pass the same host implementation.
type Runner interface {
	Exec(ctx context.Context, name string, args ...string) (Result, error)
}

// Result mirrors a completed command.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// inspectFormat pulls everything from one call. The env is dumped wholesale and
// parsed here rather than picked apart in a Go template, because docker's
// template language has no way to filter a list by prefix.
const inspectFormat = `{{.Architecture}}|{{.Os}}|{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}|{{.Created}}|{{range .Config.Env}}{{.}};{{end}}`

// Probe asks docker about ref. hostArch is the architecture the host can run,
// used to catch an image that cannot possibly start here.
func Probe(ctx context.Context, r Runner, ref, hostArch string) Facts {
	f := Facts{Ref: ref}

	res, err := r.Exec(ctx, "docker", "image", "inspect", "--format", inspectFormat, ref)
	if err != nil {
		f.Problem, f.Detail = Unknown, err.Error()
		return f
	}
	if res.Code != 0 {
		if isNoSuchImage(res.Stderr) {
			f.Problem = Absent
			return f
		}
		f.Problem, f.Detail = Unknown, strings.TrimSpace(res.Stderr)
		return f
	}

	f.Present = true
	parse(strings.TrimSpace(res.Stdout), &f)

	// Size comes from `docker images` because inspect's number is not the one
	// on disk; see Facts.Size.
	if s, err := r.Exec(ctx, "docker", "images", "--format", "{{.Size}}", ref); err == nil && s.Code == 0 {
		f.Size = firstLine(s.Stdout)
	}

	f.Problem = OK
	if hostArch != "" && f.Arch != "" && normalise(f.Arch) != normalise(hostArch) {
		f.Problem = WrongArch
	}
	return f
}

func parse(out string, f *Facts) {
	parts := strings.Split(out, "|")
	get := func(i int) string {
		if i < len(parts) {
			return strings.TrimSpace(parts[i])
		}
		return ""
	}
	f.Arch = get(0)
	f.OS = get(1)
	f.Digest = get(2)

	if t, err := time.Parse(time.RFC3339Nano, get(3)); err == nil {
		f.Created = t
	}
	for _, e := range strings.Split(get(4), ";") {
		if v, ok := strings.CutPrefix(e, "ROS_DISTRO="); ok {
			f.ROSDistro = strings.TrimSpace(v)
		}
	}
}

func isNoSuchImage(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no such image") ||
		strings.Contains(s, "no such object") ||
		strings.Contains(s, "not found")
}

func normalise(a string) string {
	switch a {
	case "aarch64", "arm64", "arm64/v8", "arm64v8":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	}
	return a
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// AgeDays is how old the image is, for reporting. Returns -1 when unknown.
func (f Facts) AgeDays(now time.Time) int {
	if f.Created.IsZero() {
		return -1
	}
	return int(now.Sub(f.Created).Hours() / 24)
}

// ShortDigest is the digest trimmed to something readable.
func (f Facts) ShortDigest() string {
	_, d, ok := strings.Cut(f.Digest, "@")
	if !ok {
		d = f.Digest
	}
	if len(d) > 19 {
		return d[:19]
	}
	return d
}

// Local reports whether this image was built here rather than pulled. A local
// build has no repo digest, which is normal for `ros2pi image build` output and
// not a problem.
func (f Facts) Local() bool { return f.Present && f.Digest == "" }

func itoa(i int) string { return strconv.Itoa(i) }
