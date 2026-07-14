package hostfacts

import (
	"fmt"
	"path"
	"strings"
)

// i2cAliasNames are the device-tree aliases that may point at the controller
// wired to header pins 3 and 5, most specific first. i2c_arm is the name
// dtparam uses; not every model defines it, so i2c1 is the documented fallback.
var i2cAliasNames = []string{"i2c_arm", "i2c1"}

// dtBaseMarkers are the path segments under which the device tree is exposed.
// An of_node symlink target always contains one; the text after it is the
// device-tree path that an alias holds.
var dtBaseMarkers = []string{"firmware/devicetree/base", "proc/device-tree"}

// i2cHeader identifies the header I2C bus WITHOUT hardcoding its number.
//
// The bus number is a kernel-assigned artefact, not a property of the hardware
// -- the same reason GPIO chip numbers are never trusted. It is resolved by
// following a device-tree alias to the controller node, then finding the
// /sys/bus/i2c/devices/i2c-N whose of_node is that same node.
//
// On this Pi 4 the alias i2c1 -> /soc/i2c@7e804000 identifies the header
// controller even while it is disabled and has no /dev/i2c-1, which is what
// lets `check` distinguish "not enabled" from "enabled on a different number".
func (p LinuxProber) i2cHeader(warns *[]Warning) I2CHeader {
	alias, dtPath := p.i2cAliasTarget()
	if dtPath == "" {
		*warns = append(*warns, Warning{
			Code: "i2c.header.assumed",
			Detail: fmt.Sprintf(
				"no device-tree alias (%s) resolved an I2C controller; assuming the "+
					"header bus is %s. Please report this with `ros2pi check --dump-facts`.",
				strings.Join(i2cAliasNames, " or "), i2cAssumedPath),
		})
		return I2CHeader{Path: i2cAssumedPath, Source: I2CAssumed}
	}

	busPath, err := p.i2cBusForDTNode(dtPath)
	if err != nil {
		// The alias names a controller that has no live bus. That is the normal
		// state when i2c is not enabled, and is NOT an error: the caller learns
		// the header bus is absent, which is exactly the intended diagnosis.
		return I2CHeader{Path: "", Source: I2CFromAlias, Alias: dtPath}
	}
	_ = alias
	return I2CHeader{Path: busPath, Source: I2CFromAlias, Alias: dtPath}
}

// i2cAliasTarget returns the first resolvable alias and the device-tree path it
// points to.
func (p LinuxProber) i2cAliasTarget() (alias, dtPath string) {
	for _, name := range i2cAliasNames {
		s, err := readTrimmed(p.IO, path.Join("proc/device-tree/aliases", name))
		if err != nil {
			continue
		}
		// Device-tree property strings are NUL-terminated.
		if s = strings.TrimRight(s, "\x00"); s != "" {
			return name, s
		}
	}
	return "", ""
}

// i2cBusForDTNode finds the /dev/i2c-N backed by the given device-tree node.
func (p LinuxProber) i2cBusForDTNode(dtPath string) (string, error) {
	const busDir = "sys/bus/i2c/devices"
	entries, err := p.IO.ReadDir(busDir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", busDir, err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "i2c-") {
			continue
		}
		link := "/" + path.Join(busDir, e.Name(), "of_node")
		target, err := p.IO.Readlink(link)
		if err != nil {
			continue
		}
		if dtPathFromOfNode(target) == dtPath {
			return "/dev/" + e.Name(), nil
		}
	}
	return "", fmt.Errorf("no live i2c bus for device-tree node %s", dtPath)
}

// dtPathFromOfNode extracts the device-tree path from an of_node symlink target.
//
// It anchors on the devicetree base marker rather than doing path arithmetic,
// because the target is relative to the link's PHYSICAL location, not its
// /sys/bus/... path: /sys/bus/i2c/devices/i2c-22 is itself a symlink into
// /sys/devices/platform/..., so joining the ../.. against the /sys/bus path
// yields /firmware/devicetree/base/... -- a directory that does not exist.
// Resolving that properly would require walking intermediate symlinks; the
// marker is exact and needs no filesystem access at all.
//
// Returns "" when the target is not a device-tree node.
func dtPathFromOfNode(target string) string {
	for _, marker := range dtBaseMarkers {
		if i := strings.Index(target, marker); i >= 0 {
			return target[i+len(marker):]
		}
	}
	return ""
}
