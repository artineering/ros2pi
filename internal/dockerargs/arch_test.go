package dockerargs_test

import (
	"go/build"
	"strings"
	"testing"
)

// allowedImports is the complete set this package may depend on.
//
// dockerargs is the pure core: (Config, HostFacts) -> argv. Purity is not a
// style preference here, it is what makes Pi 5 and rootless-docker and
// 32-bit-userland behaviour testable on hardware we do not have. The moment
// this package can stat a file, someone will make it stat a file, and the
// golden tests quietly stop proving anything.
//
// If you are here because this test failed: do not add your import. Probe the
// host in internal/hostfacts and put the answer in HostFacts.
var allowedImports = map[string]bool{
	// stdlib, all deterministic
	"crypto/sha256": true,
	"encoding/hex":  true,
	"fmt":           true,
	"sort":          true,
	"strconv":       true,
	"strings":       true,

	// our own pure packages
	"github.com/artineering/ros2pi/internal/config":    true,
	"github.com/artineering/ros2pi/internal/errs":      true,
	"github.com/artineering/ros2pi/internal/hostfacts": true,
}

// forbidden names the packages whose presence would specifically destroy
// purity, so the failure message can say why rather than just "not allowed".
var forbidden = map[string]string{
	"os":            "reads the real host; probe in hostfacts instead",
	"os/exec":       "runs commands; dockerargs decides args, it does not run them",
	"io/fs":         "reads the real host; probe in hostfacts instead",
	"time":          "non-deterministic; pass any timestamp in",
	"math/rand":     "non-deterministic",
	"net":           "talks to the world",
	"path/filepath": "resolves against the real filesystem; use path or pass values in",
}

func TestDockerargsStaysPure(t *testing.T) {
	pkg, err := build.Import("github.com/artineering/ros2pi/internal/dockerargs", "", 0)
	if err != nil {
		t.Fatalf("import dockerargs: %v", err)
	}

	for _, imp := range pkg.Imports {
		if why, bad := forbidden[imp]; bad {
			t.Errorf("dockerargs imports %q: %s", imp, why)
			continue
		}
		if !allowedImports[imp] {
			t.Errorf("dockerargs imports %q, which is not on the allow-list.\n"+
				"If it is genuinely pure and deterministic, add it to allowedImports.\n"+
				"If it touches the host, probe it in hostfacts and put the result in HostFacts.",
				imp)
		}
	}
}

// hostfacts is the seam's other half: it may touch the host, but it must not
// depend on the packages that consume it, or the dependency inverts and purity
// becomes unenforceable.
func TestHostfactsDoesNotDependOnItsConsumers(t *testing.T) {
	pkg, err := build.Import("github.com/artineering/ros2pi/internal/hostfacts", "", 0)
	if err != nil {
		t.Fatalf("import hostfacts: %v", err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, "ros2pi/internal/") {
			t.Errorf("hostfacts imports %q; it must depend on nothing of ours", imp)
		}
	}
}
