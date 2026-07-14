package hostfacts

import (
	"context"
	"os/user"
	"testing"
)

// The container's group must come from the user's passwd entry, not from the
// effective gid.
//
// This is not hypothetical: `newgrp docker` is the fix ros2pi RECOMMENDS for a
// stale session, and it sets egid to the docker group. Taking egid would write
// every build artefact as group=docker, and would silently change the container
// fingerprint once the user logged back in normally -- making a perfectly good
// container look stale and demanding a recreate that stops their nodes.
func TestIdentity_UsesPasswdPrimaryGroupNotEffectiveGID(t *testing.T) {
	p := pi5()
	base := p.host()

	// Simulate `newgrp docker`: euid is the user, but egid is the docker group.
	io := gidOverride{HostIO: base, euid: 1000, egid: 984, primaryGID: "1000"}

	f, err := NewLinuxProber(io, "test").Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Identity.UID != 1000 {
		t.Errorf("uid = %d, want 1000", f.Identity.UID)
	}
	if f.Identity.GID == 984 {
		t.Fatal("gid = 984 (the docker group): took the effective gid, so build " +
			"output would be group=docker and the fingerprint would flip on re-login")
	}
	if f.Identity.GID != 1000 {
		t.Errorf("gid = %d, want 1000 (the passwd primary group)", f.Identity.GID)
	}
}

// The identity must not depend on transient group context at all: the same host
// under `newgrp` and under a plain login must produce the same facts, or the
// container fingerprint flaps.
func TestIdentity_StableAcrossGroupContext(t *testing.T) {
	base := pi5().host()

	plain, err := NewLinuxProber(
		gidOverride{HostIO: base, euid: 1000, egid: 1000, primaryGID: "1000"}, "test").
		Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	newgrp, err := NewLinuxProber(
		gidOverride{HostIO: base, euid: 1000, egid: 984, primaryGID: "1000"}, "test").
		Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if plain.Identity != newgrp.Identity {
		t.Errorf("identity differs by group context:\n  plain:  %+v\n  newgrp: %+v",
			plain.Identity, newgrp.Identity)
	}
}

// gidOverride simulates a specific uid/gid context.
type gidOverride struct {
	HostIO
	euid, egid int
	primaryGID string
}

func (g gidOverride) Geteuid() int { return g.euid }
func (g gidOverride) Getegid() int { return g.egid }
func (g gidOverride) LookupUID(uid int) (*user.User, error) {
	return &user.User{Uid: "1000", Gid: g.primaryGID, Username: "pi"}, nil
}
