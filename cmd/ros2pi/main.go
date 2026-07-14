// Command ros2pi runs ROS 2 on a Raspberry Pi via Docker, without installing
// ROS on the host.
//
// M0 scaffold: only `check --dump-facts` is wired up so far.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/artineering/ros2pi/internal/hostfacts"
)

var version = "0.0.0-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ros2pi: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 2 && args[0] == "check" && args[1] == "--dump-facts" {
		return dumpFacts(context.Background())
	}
	return fmt.Errorf("M0 scaffold: only `ros2pi check --dump-facts` is implemented")
}

func dumpFacts(ctx context.Context) error {
	// ROS2PI_FACTS replays a fixture instead of probing, so a maintainer can
	// reproduce a reporter's host exactly without owning that hardware.
	var p hostfacts.Prober = hostfacts.NewLinuxProber(hostfacts.NewOSHost(), version)
	if v := os.Getenv("ROS2PI_FACTS"); v != "" {
		fp, err := factsProber(v)
		if err != nil {
			return err
		}
		p = fp
	}
	f, err := p.Probe(ctx)
	if err != nil {
		return err
	}
	return f.Dump(os.Stdout)
}

// factsProber builds a FileProber for an OS path, accepting relative and
// absolute forms alike.
//
// fs.FS paths are slash-separated and rooted, with no leading separator, so the
// OS path is made absolute first and the leading separator then stripped --
// rather than blindly slicing off the first character, which silently ate the
// first letter of every relative path.
func factsProber(osPath string) (hostfacts.Prober, error) {
	abs, err := filepath.Abs(osPath)
	if err != nil {
		return nil, fmt.Errorf("resolve ROS2PI_FACTS=%q: %w", osPath, err)
	}
	rel := strings.TrimPrefix(filepath.ToSlash(abs), "/")
	if rel == "" {
		return nil, fmt.Errorf("ROS2PI_FACTS=%q does not name a file", osPath)
	}
	return hostfacts.FileProber{FS: os.DirFS("/"), Path: rel}, nil
}
