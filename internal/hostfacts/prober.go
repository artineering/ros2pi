package hostfacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
)

// Prober produces HostFacts. Two implementations exist: LinuxProber reads the
// real host, FileProber replays a JSON fixture.
type Prober interface {
	Probe(ctx context.Context) (HostFacts, error)
}

// FileProber replays a HostFacts JSON fixture. It backs both the test suite and
// the ROS2PI_FACTS escape hatch, which lets a maintainer reproduce a user's
// exact host from their bug report without owning the hardware.
type FileProber struct {
	FS   fs.FS
	Path string
}

func (p FileProber) Probe(context.Context) (HostFacts, error) {
	f, err := p.FS.Open(p.Path)
	if err != nil {
		return HostFacts{}, fmt.Errorf("open facts fixture %s: %w", p.Path, err)
	}
	defer f.Close()
	return Load(f)
}

// Load decodes HostFacts and rejects fixtures from an incompatible schema
// rather than silently misreading them.
func Load(r io.Reader) (HostFacts, error) {
	var f HostFacts
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return HostFacts{}, fmt.Errorf("decode host facts: %w", err)
	}
	if f.SchemaVersion != SchemaVersion {
		return HostFacts{}, fmt.Errorf(
			"host facts schema version %d, this build expects %d",
			f.SchemaVersion, SchemaVersion)
	}
	return f, nil
}

// Dump writes HostFacts as indented JSON. This is what --dump-facts emits and
// what test fixtures store.
func (f HostFacts) Dump(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(f)
}
