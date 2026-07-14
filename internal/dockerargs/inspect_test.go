package dockerargs

import (
	"strings"
	"testing"

	"github.com/artineering/ros2pi/internal/config"
)

// `docker inspect NAME` searches containers AND images. `ros2pi image build`
// creates an image named after the same workspace as the container, so without
// --type container a bare inspect resolves to that image once the container is
// gone -- and the format template dies on a missing .State with an error that
// has nothing to do with what the user did.
func TestInspectArgsRestrictsToContainers(t *testing.T) {
	cfg := config.Default()
	cfg.Root = "/home/pi/ws"

	args := strings.Join(InspectArgs(cfg).Args, " ")
	if !strings.Contains(args, "--type container") {
		t.Errorf("inspect must be restricted to containers, or it can resolve to "+
			"the workspace image built by `ros2pi image build`:\n  %s", args)
	}
}
