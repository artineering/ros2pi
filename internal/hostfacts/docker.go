package hostfacts

import (
	"context"
	"strings"
)

// dockerInfoFormat pulls everything needed in one daemon round-trip. Fields are
// pipe-separated because docker's --format is Go templates, and a struct output
// would need JSON parsing of a shape that varies across versions.
const dockerInfoFormat = `{{.ServerVersion}}|{{.Architecture}}|{{.CgroupVersion}}|{{.SecurityOptions}}`

// docker probes the one dependency ros2pi has.
//
// The goal is not "does docker work" but "WHY does it not work", because the
// distinct failures need opposite fixes and Docker itself reports several of
// them with near-identical text. Nothing here decides what to tell the user --
// it records a classification that the pure diagnostic layer turns into advice.
func (p LinuxProber) docker(ctx context.Context, groups []Group) DockerFacts {
	var d DockerFacts

	bin, err := p.IO.LookPath("docker")
	if err != nil {
		d.Problem = DockerAbsent
		return d
	}
	d.Binary = bin

	// Client version does not need the daemon, so it is available even when
	// everything else fails -- useful in a bug report.
	if r, err := p.IO.Exec(ctx, "docker", "version", "--format", "{{.Client.Version}}"); err == nil && r.Code == 0 {
		d.ClientVersion = strings.TrimSpace(r.Stdout)
	}

	r, err := p.IO.Exec(ctx, "docker", "info", "--format", dockerInfoFormat)
	if err != nil {
		d.Problem, d.Detail = DockerUnknownFailure, err.Error()
		return d
	}
	if r.Code != 0 {
		d.Problem = classifyDockerError(r.Stderr, groups)
		if d.Problem == DockerUnknownFailure {
			d.Detail = truncate(strings.TrimSpace(r.Stderr), 400)
		}
		return d
	}

	d.Problem = DockerOK
	parseDockerInfo(strings.TrimSpace(r.Stdout), &d)
	return d
}

func parseDockerInfo(out string, d *DockerFacts) {
	parts := strings.Split(out, "|")
	get := func(i int) string {
		if i < len(parts) {
			return strings.TrimSpace(parts[i])
		}
		return ""
	}
	d.ServerVersion = get(0)
	d.ServerArch = get(1)
	d.CgroupVersion = get(2)
	// SecurityOptions renders as a Go slice literal, e.g.
	// [name=seccomp,profile=builtin name=rootless]
	d.Rootless = strings.Contains(get(3), "name=rootless")
}

// classifyDockerError maps docker's stderr onto a problem.
//
// Matching on message text is unpleasant but unavoidable: docker exits 1 for
// every one of these, and the distinction is exactly what the user needs. The
// strings are matched case-insensitively and loosely, and anything unrecognised
// becomes DockerUnknownFailure with the raw text preserved rather than being
// forced into a bucket that would produce confidently wrong advice.
func classifyDockerError(stderr string, groups []Group) DockerProblem {
	s := strings.ToLower(stderr)

	switch {
	case strings.Contains(s, "permission denied") &&
		(strings.Contains(s, "docker.sock") || strings.Contains(s, "docker api") ||
			strings.Contains(s, "docker daemon")):
		// The database and this process can disagree; that disagreement is the
		// whole diagnosis.
		if g, ok := findGroup(groups, "docker"); ok && g.MembershipIsStale() {
			return DockerPermissionStaleSession
		}
		return DockerPermissionDenied

	case strings.Contains(s, "cannot connect to the docker daemon"),
		strings.Contains(s, "is the docker daemon running"),
		strings.Contains(s, "docker daemon is not running"):
		return DockerDaemonUnreachable
	}
	return DockerUnknownFailure
}

func findGroup(groups []Group, name string) (Group, bool) {
	for _, g := range groups {
		if g.Name == name {
			return g, true
		}
	}
	return Group{}, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
