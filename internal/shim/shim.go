// Package shim provides the script that runs inside the container.
//
// It is embedded in the binary and materialised to a cache directory at run
// time, then bind-mounted read-only into the container. That is deliberate: a
// real file is debuggable (a user can cat it), and `ros2pi shell` can use it as
// an --rcfile, neither of which works if the script is a string smuggled
// through `bash -c`.
package shim

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed entry.sh
var entrySH []byte

// FileName is the shim's name inside its directory and in the container.
const FileName = "entry.sh"

// Materialise writes the shim under cacheDir and returns the directory to
// bind-mount.
//
// The content hash is part of the path so that an upgraded ros2pi cannot be
// served a stale shim from a previous version's cache, and so concurrent
// invocations never write the same file.
func Materialise(cacheDir string) (string, error) {
	sum := sha256.Sum256(entrySH)
	dir := filepath.Join(cacheDir, "shim", hex.EncodeToString(sum[:])[:12])

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create shim dir: %w", err)
	}
	path := filepath.Join(dir, FileName)

	// Content is addressed by hash, so an existing file is by definition the
	// right one.
	if _, err := os.Stat(path); err == nil {
		return dir, nil
	}

	// Write via a temp file in the same directory, then rename: a concurrent
	// ros2pi must never observe a half-written shim.
	tmp, err := os.CreateTemp(dir, ".entry-*")
	if err != nil {
		return "", fmt.Errorf("create shim temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(entrySH); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write shim: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close shim: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return "", fmt.Errorf("chmod shim: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("install shim: %w", err)
	}
	return dir, nil
}

// Source returns the shim's contents, for tests and `ros2pi shell --print-shim`.
func Source() []byte { return entrySH }
