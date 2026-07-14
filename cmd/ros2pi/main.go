// Command ros2pi runs ROS 2 on a Raspberry Pi via Docker, without installing
// ROS on the host.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/artineering/ros2pi/internal/cli"
)

// version is overridden at build time: -ldflags "-X main.version=v0.1.0"
var version = "0.0.0-dev"

func main() {
	// Ctrl-C must reach the process inside the container, not kill us first and
	// orphan it. docker forwards the signal to the container; cancelling the
	// context lets the child exit on its own terms and report its code.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := cli.App{
		Version: version,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		IsTTY:   isTTY,
	}
	os.Exit(app.Execute(ctx, os.Args[1:]))
}

// isTTY reports whether BOTH stdin and stdout are terminals.
//
// Both, not either: docker exec -t on a piped stdout injects control characters
// into the stream, which corrupts `ros2pi topic echo | head` in ways that look
// like a ROS bug rather than a ros2pi one.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
