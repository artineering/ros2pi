// Package dockerargs turns a Config and a HostFacts into a docker command line.
//
// Every function here is PURE: no filesystem, no exec, no clock, no randomness.
// That is a hard constraint, enforced by arch_test.go, and it is what makes the
// hard cases testable -- a Pi 5 on kernel 6.6, a host with rootless docker, a
// 32-bit userland -- none of which we can hold in our hands.
//
// It follows that every reason to fail is discovered HERE, from data, rather
// than by running docker and reading its stderr. "i2c is not enabled" is a
// conclusion about the host, not a docker error.
package dockerargs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/artineering/ros2pi/internal/config"
	"github.com/artineering/ros2pi/internal/errs"
	"github.com/artineering/ros2pi/internal/hostfacts"
)

// ShimDir is where the entry shim is mounted inside the container.
const ShimDir = "/usr/local/lib/ros2pi"

// WorkspaceDir is where the host workspace is mounted inside the container.
const WorkspaceDir = "/ros2_ws"

// Label keys. The container's own labels are the source of truth about what it
// was created for; there is no state file to drift.
const (
	LabelWorkspace = "io.ros2pi.workspace"
	LabelPlan      = "io.ros2pi.plan"
	LabelImage     = "io.ros2pi.image"
	LabelVersion   = "io.ros2pi.version"
)

// Decision records why one argument exists. It is what makes a golden file
// reviewable: the diff says what behaviour changed, not which byte moved.
type Decision struct {
	Arg    string // the flag, or the first of a pair
	Reason string // why it is there, in one line
	Source string // the fact that caused it, e.g. "facts.Groups[i2c]"
}

// Plan is a docker invocation plus the reasoning behind it.
type Plan struct {
	Args      []string
	Container string
	Image     string
	Decisions []Decision
	Warnings  []string

	// Fingerprint identifies the container configuration: two plans with the
	// same fingerprint describe the same container, so a running container
	// whose io.ros2pi.plan label differs is stale.
	//
	// It is a field rather than a method over Args because the fingerprint is
	// itself stored in Args as a label -- hashing Args would be circular. It
	// covers the create flags BEFORE that label is appended.
	//
	// It hashes the ARGS, not the Config, so that editing [build] or [env] --
	// which do not affect the container -- never forces a recreate.
	Fingerprint string
}

// fingerprint hashes an argument list.
func fingerprint(args []string) string {
	h := sha256.New()
	for _, a := range args {
		h.Write([]byte(a))
		h.Write([]byte{0}) // unambiguous separator: "a","bc" != "ab","c"
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:16]
}

type builder struct {
	args      []string
	decisions []Decision
	warnings  []string
}

func (b *builder) add(reason, source string, args ...string) {
	if len(args) == 0 {
		return
	}
	b.decisions = append(b.decisions, Decision{Arg: args[0], Reason: reason, Source: source})
	b.args = append(b.args, args...)
}

func (b *builder) warn(format string, a ...any) {
	b.warnings = append(b.warnings, fmt.Sprintf(format, a...))
}

// ContainerName derives a stable, human-readable name from the workspace path.
//
// The hash disambiguates two workspaces with the same basename; the basename
// keeps `docker ps` readable. It is derived from the path alone so that it
// survives config changes -- a recreate reuses the name rather than orphaning
// the old container.
func ContainerName(absWorkspace string) string {
	sum := sha256.Sum256([]byte(absWorkspace))
	base := absWorkspace
	if i := strings.LastIndexByte(base, '/'); i >= 0 && i < len(base)-1 {
		base = base[i+1:]
	}
	base = sanitize(base)
	if base == "" {
		base = "ws"
	}
	return "ros2pi-" + base + "-" + hex.EncodeToString(sum[:])[:8]
}

// sanitize keeps only characters docker accepts in a container name.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// CreateArgs builds the `docker create` command line for a workspace container.
//
// The container is long-lived and does nothing (`sleep infinity`); work happens
// via `docker exec`. That is not an optimisation: a fresh `docker run` per
// command measured 5.3s against 2.0s for exec on a Pi 4, and it also discards
// the ROS daemon's discovery state between commands.
func CreateArgs(cfg config.Config, f hostfacts.HostFacts, version string) (Plan, error) {
	if err := checkArch(f); err != nil {
		return Plan{}, err
	}

	name := ContainerName(cfg.Root)
	b := &builder{}

	b.add("create a container without starting it", "", "create")
	b.add("name derived from the workspace path", "cfg.Root", "--name", name)

	// PID 1 must reap and forward signals. The ROS image's entrypoint execs, so
	// `sleep` becomes PID 1, where SIGTERM's default disposition does not apply
	// -- without --init every `down` would hang 10s and then SIGKILL.
	b.add("reap zombies and forward signals to PID 1", "", "--init")

	if err := addIdentity(b, f); err != nil {
		return Plan{}, err
	}
	addNetwork(b, cfg)
	addMounts(b, cfg)
	addEnv(b, cfg, f)
	if err := addHardware(b, cfg, f); err != nil {
		return Plan{}, err
	}
	addLabels(b, cfg, f, version)

	// The fingerprint covers everything decided so far. It must be computed
	// before it is itself added as a label, or the hash would cover its own
	// value.
	fp := fingerprint(b.args)
	b.add("fingerprint of this configuration; a differing label means stale",
		"", "--label", LabelPlan+"="+fp)

	b.add("the ROS image this workspace targets", "cfg.ROS.Image", cfg.ROS.Image)
	// Keep the container alive doing nothing; exec does the work.
	b.add("idle forever; work arrives via docker exec", "", "sleep", "infinity")

	p := Plan{
		Args:        b.args,
		Container:   name,
		Image:       cfg.ROS.Image,
		Decisions:   b.decisions,
		Warnings:    b.warnings,
		Fingerprint: fp,
	}
	return p, nil
}

// checkArch refuses hosts that cannot run the ROS images at all.
//
// All three architecture views must agree. The classic Pi trap is a 64-bit
// kernel with a 32-bit userland: uname says aarch64 while dpkg says armhf and
// the docker daemon is 32-bit, so trusting uname alone produces a container
// that cannot start.
func checkArch(f hostfacts.HostFacts) error {
	views := map[string]string{
		"kernel (uname -m)":            normalizeArch(f.Arch.Machine),
		"userland (dpkg --print-arch)": normalizeArch(f.Arch.Dpkg),
		"ros2pi binary":                normalizeArch(f.Arch.Go),
	}
	if f.Docker.ServerArch != "" {
		views["docker daemon"] = normalizeArch(f.Docker.ServerArch)
	}

	bad := map[string]string{}
	for k, v := range views {
		if v != "" && v != "arm64" && v != "amd64" {
			bad[k] = v
		}
	}
	if len(bad) > 0 {
		e := errs.New(errs.CodeArchUnsupported, "this host cannot run ROS 2 containers").
			WithDoc("https://github.com/artineering/ros2pi#arm64-only--and-that-is-not-our-choice")
		for _, k := range sortedKeys(bad) {
			e.WithDetail("%-28s %s", k+":", bad[k])
		}
		e.WithDetail("")
		e.WithDetail("ROS 2 images are published for arm64 and amd64 only -- there is no")
		e.WithDetail("armv7 manifest -- and Docker Engine v29 dropped armhf packages for")
		e.WithDetail("Raspberry Pi OS. This is closed upstream, not a ros2pi limitation.")
		e.WithFix(&errs.Fix{Steps: []errs.Step{
			{Text: "Reflash with 64-bit Raspberry Pi OS (Pi 3 and newer support it)."},
		}})
		return e
	}
	return nil
}

func normalizeArch(a string) string {
	switch a {
	case "aarch64", "arm64", "arm64/v8", "arm64v8":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	case "":
		return ""
	}
	return a
}

// addIdentity runs the container as the invoking user, so files written into
// the bind mount stay editable from the host.
func addIdentity(b *builder, f hostfacts.HostFacts) error {
	id := f.Identity
	if id.UID == 0 {
		// Refusing is better than producing a workspace full of root-owned
		// files that the user then cannot delete without sudo.
		return errs.New(errs.CodeConfigInvalid, "refusing to run the container as root").
			WithDetail("resolved uid 0; the workspace would fill with root-owned files").
			WithFix(&errs.Fix{Steps: []errs.Step{
				{Text: "run ros2pi as your normal user, not with sudo"},
			}})
	}
	b.add("run as the invoking user so bind-mounted files stay editable",
		"facts.Identity", "--user", fmt.Sprintf("%d:%d", id.UID, id.GID))
	return nil
}

func addNetwork(b *builder, cfg config.Config) {
	b.add("DDS discovery needs the host network to reach peers",
		"cfg.ROS.Network", "--network", cfg.ROS.Network)
	b.add("DDS shared-memory transport needs host IPC",
		"cfg.ROS.IPC", "--ipc", cfg.ROS.IPC)
}

func addMounts(b *builder, cfg config.Config) {
	b.add("the workspace itself", "cfg.Root",
		"-v", cfg.Root+":"+WorkspaceDir)
	b.add("start in the workspace", "", "-w", WorkspaceDir)
	for _, m := range cfg.Mounts.Extra {
		b.add("extra mount from config", "cfg.Mounts.Extra", "-v", m)
	}
}

func addEnv(b *builder, cfg config.Config, f hostfacts.HostFacts) {
	b.add("DDS domain; two workspaces on one Pi collide unless this differs",
		"cfg.ROS.DomainID", "-e", "ROS_DOMAIN_ID="+strconv.Itoa(cfg.ROS.DomainID))

	// The shim asserts this matches, rather than trusting `source` to fail --
	// the image entrypoint has already sourced its own distro by then.
	b.add("the distro the workspace expects; the shim verifies it",
		"cfg.ROS.Distro", "-e", "ROS2PI_EXPECT_DISTRO="+cfg.ROS.Distro)

	if cfg.ROS.RMW != "" {
		b.add("RMW implementation from config", "cfg.ROS.RMW",
			"-e", "RMW_IMPLEMENTATION="+cfg.ROS.RMW)
	}
	for _, k := range sortedKeys(cfg.Env) {
		b.add("env from config", "cfg.Env", "-e", k+"="+cfg.Env[k])
	}
}

func addLabels(b *builder, cfg config.Config, f hostfacts.HostFacts, version string) {
	b.add("workspace this container serves", "", "--label", LabelWorkspace+"="+cfg.Root)
	b.add("image at creation time", "", "--label", LabelImage+"="+cfg.ROS.Image)
	b.add("ros2pi version that created it", "", "--label", LabelVersion+"="+version)
}

// sortedKeys makes map iteration deterministic. Any map that reaches argv must
// go through here: Go randomises map order, and a flapping argv would flap the
// plan fingerprint, making a running container intermittently look stale.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
