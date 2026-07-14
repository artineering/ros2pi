package hostfacts

import (
	"context"
	"encoding/json"
	"os/user"
	"strings"
	"testing"
)

// SUDO_UID without SUDO_GID must NOT silently fall back to root. Running as
// root is the exact bug this tool exists to prevent, and doing it with
// UnderSudo=false would leave nothing downstream able to notice.
func TestIdentity_SudoUIDWithoutSudoGIDDoesNotFallBackToRoot(t *testing.T) {
	p := pi5()
	p.env = map[string]string{"SUDO_UID": "1000"} // SUDO_GID deliberately absent

	env := override{HostIO: p.host(), lookupUID: func(int) (*user.User, error) {
		return &user.User{Uid: "1000", Gid: "1000", Username: "pi"}, nil
	}}

	f, err := NewLinuxProber(env, "test").Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Identity.UID != 1000 {
		t.Errorf("uid = %d, want 1000", f.Identity.UID)
	}
	if f.Identity.GID == 0 {
		t.Error("gid fell back to 0 (root); workspace files would be root-owned")
	}
	if !f.Identity.UnderSudo {
		t.Error("UnderSudo = false; the sudo context was lost entirely")
	}
	if !hasWarning(f, "identity.sudo_gid.missing") {
		t.Errorf("expected identity.sudo_gid.missing warning, got %+v", f.Warnings)
	}
}

// An unknown family must not silently probe the Pi 4 gpiomem layout. Guessing
// here would contradict markHeaderChip, which refuses to guess about the very
// same unknown host.
func TestGPIOMem_UnknownFamilyRefusesToGuessLayout(t *testing.T) {
	p := pi5()
	p.chips = map[string]GPIOChipInfoResult{
		"/dev/gpiochip0": {Name: "gpiochip0", Label: "pinctrl-rp2-hypothetical", Lines: 54},
	}
	f, _ := NewLinuxProber(p.host(), "test").Probe(context.Background())

	if f.Model.Family != FamilyUnknown {
		t.Fatalf("family = %q, want unknown", f.Model.Family)
	}
	if len(f.GPIOMem) != 0 {
		t.Errorf("gpiomem = %+v, want nothing probed on unknown hardware", f.GPIOMem)
	}
	if !hasWarning(f, "gpio.mem.unknown") {
		t.Errorf("expected gpio.mem.unknown warning, got %+v", f.Warnings)
	}
}

// NameResolvable must be computed from the host GID, not asserted from the
// name. A host whose dialout is not 20 would otherwise get `--group-add dialout`,
// which resolves to 20 inside the container while the device is owned by the
// host GID -- EPERM, silently.
func TestGroups_NameResolvableIsComputedNotAssumed(t *testing.T) {
	p := pi5()
	p.groups = map[string]string{
		"dialout": "1234", // an admin moved it off the Debian-stable 20
		"video":   "44",   // matches the container
		"gpio":    "993",
	}
	f, _ := NewLinuxProber(p.host(), "test").Probe(context.Background())

	d, _ := f.Group("dialout")
	if d.NameResolvable {
		t.Error("dialout claimed name-resolvable at gid 1234; the container resolves it to 20")
	}
	if arg, ok := d.GroupAddArg(); !ok || arg != "1234" {
		t.Errorf("dialout --group-add = %q, want the numeric 1234", arg)
	}

	v, _ := f.Group("video")
	if !v.NameResolvable {
		t.Error("video at the stable gid 44 should be name-resolvable")
	}
	if arg, _ := v.GroupAddArg(); arg != "video" {
		t.Errorf("video --group-add = %q, want the name", arg)
	}

	g, _ := f.Group("gpio")
	if g.NameResolvable {
		t.Error("gpio is never in the ROS image and can never resolve by name")
	}
	if arg, _ := g.GroupAddArg(); arg != "993" {
		t.Errorf("gpio --group-add = %q, want the numeric 993", arg)
	}
}

// A group that does not exist on the host has a meaningless GID and must not
// produce a --group-add flag at all.
func TestGroups_AbsentGroupYieldsNoArg(t *testing.T) {
	p := pi5()
	p.groups = map[string]string{"gpio": "993"} // no i2c on this host
	f, _ := NewLinuxProber(p.host(), "test").Probe(context.Background())

	i2c, ok := f.Group("i2c")
	if !ok {
		t.Fatal("i2c should still be reported, as absent")
	}
	if i2c.Exists {
		t.Error("i2c reported as existing")
	}
	if _, ok := i2c.GroupAddArg(); ok {
		t.Error("an absent group produced a --group-add argument")
	}
}

// Groups must be deterministically ordered. A map would randomise --group-add
// ordering between runs, flapping the plan fingerprint and making a running
// container intermittently look stale.
func TestGroups_OrderIsDeterministic(t *testing.T) {
	var first []string
	for i := 0; i < 50; i++ {
		f, _ := NewLinuxProber(pi5().host(), "test").Probe(context.Background())
		var names []string
		for _, g := range f.Groups {
			names = append(names, g.Name)
		}
		if i == 0 {
			first = names
			continue
		}
		if strings.Join(names, ",") != strings.Join(first, ",") {
			t.Fatalf("group order flapped between runs:\n  %v\n  %v", first, names)
		}
	}
	if !sortedStrings(first) {
		t.Errorf("groups = %v, want sorted by name", first)
	}
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}

// HostFacts is the immutable input to the pure argv core. Accessors must not
// hand out a window into the shared backing array.
func TestAccessors_ReturnCopiesNotAliases(t *testing.T) {
	f, _ := NewLinuxProber(pi5().host(), "test").Probe(context.Background())

	c, ok := f.HeaderChip()
	if !ok {
		t.Fatal("no header chip")
	}
	c.Label = "mutated"

	again, _ := f.HeaderChip()
	if again.Label == "mutated" {
		t.Error("mutating the returned chip changed the facts; accessor aliases shared state")
	}
}

// The facts must survive a JSON round-trip unchanged: fixtures captured from
// user bug reports are replayed through exactly this path.
func TestHostFacts_JSONRoundTrip(t *testing.T) {
	orig, _ := NewLinuxProber(pi5().host(), "test").Probe(context.Background())

	var buf strings.Builder
	if err := orig.Dump(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Load(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("round-trip failed: %v", err)
	}

	a, _ := json.Marshal(orig)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Error("facts changed across a JSON round-trip")
	}
}

// Load must reject a fixture from an incompatible schema rather than silently
// misreading it as a zero value.
func TestLoad_RejectsWrongSchemaVersion(t *testing.T) {
	_, err := Load(strings.NewReader(`{"schema_version": 99}`))
	if err == nil {
		t.Fatal("accepted a fixture from schema 99")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Errorf("error = %v, want it to name the schema mismatch", err)
	}
}
