package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ROS2PI_FACTS is how a maintainer replays a bug reporter's host, so the first
// thing typed is likely a relative path. Slicing off the first character to
// strip a leading slash silently ate it, and the error then blamed a filename
// the user never wrote.
func TestFactsProber_AcceptsRelativeAndAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "facts.json")
	// A distinctive value proves the file was actually found and read, rather
	// than an error merely being absent.
	const marker = "Raspberry Pi 5 Model B Rev 1.0"
	body := `{"schema_version":1,"model":{"raw":"` + marker + `","family":"pi5"}}`
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	for _, in := range []string{"facts.json", "./facts.json", abs} {
		t.Run(in, func(t *testing.T) {
			p, err := factsProber(in)
			if err != nil {
				t.Fatalf("factsProber(%q) errored: %v", in, err)
			}
			f, err := p.Probe(t.Context())
			if err != nil {
				if strings.Contains(err.Error(), "no such file") {
					t.Fatalf("path was mangled, file not found: %v", err)
				}
				t.Fatalf("probe failed: %v", err)
			}
			if f.Model.Raw != marker {
				t.Errorf("model = %q, want %q: the fixture was not read", f.Model.Raw, marker)
			}
		})
	}
}

// A value that resolves to the filesystem root names no file; say so plainly
// rather than failing later with an opaque message.
func TestFactsProber_RejectsRootPath(t *testing.T) {
	if _, err := factsProber("/"); err == nil {
		t.Fatal(`factsProber("/") should error: it names no file`)
	}
}
