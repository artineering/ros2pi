// Package config loads a workspace's ros2pi.toml.
//
// The file is the user's statement of intent ("I want jazzy, with i2c"). It is
// never merged with what the host actually provides -- that comparison happens
// downstream, where a conflict becomes a diagnosis rather than a silent
// fallback.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/artineering/ros2pi/internal/errs"
)

// FileName is the marker that makes a directory a ros2pi workspace.
const FileName = "ros2pi.toml"

// SchemaVersion is the config format version. Bumped only on a breaking change.
const SchemaVersion = 1

// Config is a workspace's declared intent.
type Config struct {
	Version  int               `toml:"version"`
	ROS      ROS               `toml:"ros"`
	Hardware Hardware          `toml:"hardware"`
	Build    Build             `toml:"build"`
	Env      map[string]string `toml:"env"`
	Mounts   Mounts            `toml:"mounts"`

	// Root is the absolute workspace directory. Set by Load, not by the file.
	Root string `toml:"-"`
}

type ROS struct {
	// Distro is the ROS 2 distribution the workspace targets. It is checked
	// against the image at run time rather than trusted: a mismatch otherwise
	// succeeds silently, because the ROS image's entrypoint has already sourced
	// its own distro by the time our shim runs.
	Distro string `toml:"distro"`

	Image string `toml:"image"`

	// Network and IPC default to host because DDS needs them: host networking
	// for peer discovery across machines, host IPC for shared-memory transport.
	Network string `toml:"network"`
	IPC     string `toml:"ipc"`

	// DomainID isolates DDS graphs. Two workspaces on one Pi collide unless
	// this differs, because --network host puts them on the same wire.
	DomainID int `toml:"domain_id"`

	RMW string `toml:"rmw"`
}

// Hardware is what the workspace wants access to. Each is a request, not an
// assertion: dockerargs errors if the host cannot honour it.
type Hardware struct {
	GPIO      bool `toml:"gpio"`
	I2C       bool `toml:"i2c"`
	SPI       bool `toml:"spi"`
	UART      bool `toml:"uart"`
	USBSerial bool `toml:"usb_serial"`

	ExtraDevices []string `toml:"extra_devices"`
	ExtraGroups  []string `toml:"extra_groups"`

	// Privileged is an escape hatch. It exists because someone will need it at
	// 2am, not because it is a supported way to run a robot; it warns loudly.
	Privileged bool `toml:"privileged"`
}

type Build struct {
	ColconArgs      []string `toml:"colcon_args"`
	ParallelWorkers int      `toml:"parallel_workers"`
}

type Mounts struct {
	Extra []string `toml:"extra"`
}

// Default is the config a fresh `ros2pi init` writes.
func Default() Config {
	return Config{
		Version: SchemaVersion,
		ROS: ROS{
			Distro:   "jazzy",
			Image:    "ros:jazzy",
			Network:  "host",
			IPC:      "host",
			DomainID: 0,
		},
		Build: Build{ColconArgs: []string{"--symlink-install"}},
		Env:   map[string]string{},
	}
}

// FindRoot walks up from dir looking for a ros2pi.toml, like git finds .git.
// Returns the directory containing it.
func FindRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, FileName)); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs { // reached /
			return "", errs.New(errs.CodeNoWorkspace,
				"not inside a ros2pi workspace").
				WithDetail("no %s found in %s or any parent directory", FileName, dir).
				WithFix(&errs.Fix{Steps: []errs.Step{
					{Text: "create one here:", Cmd: "ros2pi init"},
				}})
		}
		abs = parent
	}
}

// Load reads and validates the config for the workspace containing dir.
func Load(dir string) (Config, error) {
	root, err := FindRoot(dir)
	if err != nil {
		return Config{}, err
	}
	return LoadFile(filepath.Join(root, FileName))
}

// LoadFile reads one config file.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, errs.New(errs.CodeNoWorkspace, "no ros2pi workspace here").
				WithDetail("%s does not exist", path).
				WithFix(&errs.Fix{Steps: []errs.Step{{Cmd: "ros2pi init"}}})
		}
		return Config{}, err
	}

	// Start from Default so an omitted key means "the sensible thing" rather
	// than the zero value -- network="" would be a silently different container.
	c := Default()
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields() // a typo'd key must not be silently ignored
	if err := dec.Decode(&c); err != nil {
		return Config{}, decodeError(path, err)
	}

	c.Root = filepath.Dir(path)
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// decodeError turns go-toml's rich error into an Actionable, keeping the
// caret-pointed source snippet that makes a typo obvious.
func decodeError(path string, err error) error {
	a := errs.New(errs.CodeConfigInvalid, "ros2pi.toml is not valid").
		WithDetail("in %s", path).
		WithCause(err)

	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, col := de.Position()
		a.WithDetail("line %d, column %d: %s", row, col, de.Error())
		if s := de.String(); s != "" {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				a.WithDetail("  %s", line)
			}
		}
		return a
	}

	var se *toml.StrictMissingError
	if errors.As(err, &se) {
		a.WithDetail("unknown key -- check for a typo")
		for _, line := range strings.Split(strings.TrimRight(se.String(), "\n"), "\n") {
			a.WithDetail("  %s", line)
		}
		return a
	}
	a.WithDetail("%v", err)
	return a
}

// Validate rejects configs that cannot produce a working container.
func (c Config) Validate() error {
	if c.Version != SchemaVersion {
		return errs.New(errs.CodeConfigInvalid, "unsupported config version").
			WithDetail("version = %d, this build understands %d", c.Version, SchemaVersion)
	}
	if c.ROS.Distro == "" {
		return errs.New(errs.CodeConfigInvalid, "ros.distro is empty")
	}
	if c.ROS.Image == "" {
		return errs.New(errs.CodeConfigInvalid, "ros.image is empty")
	}
	switch c.ROS.Network {
	case "host", "bridge", "none":
	default:
		return errs.New(errs.CodeConfigInvalid, "ros.network is not a docker network mode").
			WithDetail("got %q, want host, bridge or none", c.ROS.Network)
	}
	switch c.ROS.IPC {
	case "host", "private", "shareable":
	default:
		return errs.New(errs.CodeConfigInvalid, "ros.ipc is not a docker ipc mode").
			WithDetail("got %q, want host, private or shareable", c.ROS.IPC)
	}
	if c.ROS.DomainID < 0 || c.ROS.DomainID > 232 {
		return errs.New(errs.CodeConfigInvalid, "ros.domain_id is out of range").
			WithDetail("got %d, want 0-232", c.ROS.DomainID)
	}
	for _, m := range c.Mounts.Extra {
		if n := len(strings.Split(m, ":")); n < 2 || n > 3 {
			return errs.New(errs.CodeConfigInvalid, "mounts.extra entry is malformed").
				WithDetail("got %q, want host:container[:ro]", m)
		}
	}
	return nil
}

// Marshal renders the config as TOML for `ros2pi init`.
func Marshal(c Config) ([]byte, error) {
	b, err := toml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return b, nil
}
