package participant

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

var (
	asha  = uuid.MustParse("6f1c2b8e-3d4a-4f5b-9c6d-7e8f9a0b1c2d")
	bilal = uuid.MustParse("0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d")
	guest = uuid.MustParse("11111111-2222-4333-8444-555555555555")
)

func TestParse(t *testing.T) {
	cases := []struct {
		identity string
		want     Identity
	}{
		{asha.String(), Identity{Kind: Member, UserID: asha}},
		{asha.String() + ":k3J9xQ2mP0aZ7bWc", Identity{Kind: Member, UserID: asha, Device: "k3J9xQ2mP0aZ7bWc"}},
		{asha.String() + ":a-b_c", Identity{Kind: Member, UserID: asha, Device: "a-b_c"}},
		{GuestPrefix + guest.String(), Identity{Kind: Guest, UserID: guest}},
		{NoteTakerPrefix + uuid.NewString(), Identity{Kind: NoteTaker}},
		// Not ours, or not well formed: nobody.
		{"", Identity{}},
		{"EG_abc123", Identity{}},
		{asha.String() + ":", Identity{}},
		{asha.String() + ":has space", Identity{}},
		{asha.String() + ":a:b", Identity{}},
		{asha.String() + ":" + strings.Repeat("a", 33), Identity{}},
		{"{" + asha.String() + "}", Identity{}},
		{"urn:uuid:" + asha.String(), Identity{}},
		{GuestPrefix + "nope", Identity{}},
	}
	for _, c := range cases {
		if got := Parse(c.identity); got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.identity, got, c.want)
		}
	}
}

func TestMemberIdentityRoundTrips(t *testing.T) {
	id := MemberIdentity(asha.String(), "laptop1")
	if id != asha.String()+":laptop1" {
		t.Fatalf("identity = %q", id)
	}
	if p := Parse(id); p.Kind != Member || p.UserID != asha || p.Device != "laptop1" {
		t.Fatalf("read back as %+v", p)
	}
	// An older page names no device, and a bad one is not trusted: the bare user id.
	for _, device := range []string{"", "a:b", "white space", "../x"} {
		if got := MemberIdentity(asha.String(), device); got != asha.String() {
			t.Errorf("MemberIdentity with device %q = %q, want the bare user id", device, got)
		}
	}
}

func TestPersonIsTheSameForEveryDevice(t *testing.T) {
	laptop, phone := MemberIdentity(asha.String(), "laptop1"), MemberIdentity(asha.String(), "phone1")
	if Person(laptop) != Person(phone) || Person(phone) != Person(asha.String()) || Person(laptop) != asha.String() {
		t.Fatalf("persons differ: %q %q %q", Person(laptop), Person(phone), Person(asha.String()))
	}
	if Person(GuestPrefix+guest.String()) != GuestPrefix+guest.String() {
		t.Fatal("a guest is their own person")
	}
	if Person(NoteTakerPrefix+"x") != "" || Person("EG_1") != "" {
		t.Fatal("the note-taker and identities not ours are nobody")
	}
	if !IsMember(phone, asha) || IsMember(phone, bilal) || IsMember(GuestPrefix+asha.String(), asha) {
		t.Fatal("IsMember")
	}
}

func TestCountPeopleCountsEachPersonOnce(t *testing.T) {
	ids := []string{
		MemberIdentity(asha.String(), "laptop1"),
		MemberIdentity(asha.String(), "phone1"),
		asha.String(), // an older page of hers
		MemberIdentity(bilal.String(), "laptop2"),
		GuestPrefix + guest.String(),
		NoteTakerPrefix + uuid.NewString(),
		"EG_recording",
	}
	if got := CountPeople(ids); got != 3 {
		t.Fatalf("CountPeople = %d, want 3 (Asha, Bilal and the guest)", got)
	}
	// One person with two devices is still one: alone, and not a meeting.
	if got := CountPeople(ids[:2]); got != 1 {
		t.Fatalf("two devices of one person = %d people, want 1", got)
	}
	if CountPeople(nil) != 0 {
		t.Fatal("an empty room has nobody")
	}
}

func TestAnybodyButKeepsTheCallWhileAnyDeviceIsIn(t *testing.T) {
	laptop, phone := MemberIdentity(asha.String(), "laptop1"), MemberIdentity(asha.String(), "phone1")
	notes := NoteTakerPrefix + uuid.NewString()
	if AnybodyBut([]string{laptop, notes}, laptop) {
		t.Fatal("alone with the note-taker is alone")
	}
	if !AnybodyBut([]string{laptop, phone, notes}, laptop) {
		t.Fatal("her phone is still in the call, so it must not end")
	}
	if !AnybodyBut([]string{laptop, MemberIdentity(bilal.String(), "x")}, laptop) {
		t.Fatal("somebody else is in")
	}
	if AnybodyBut(nil, laptop) {
		t.Fatal("an empty room")
	}
}

func TestDevicesOf(t *testing.T) {
	laptop, phone := MemberIdentity(asha.String(), "laptop1"), MemberIdentity(asha.String(), "phone1")
	got := DevicesOf([]string{laptop, MemberIdentity(bilal.String(), "x"), phone, GuestPrefix + asha.String()}, asha)
	if len(got) != 2 || got[0] != laptop || got[1] != phone {
		t.Fatalf("DevicesOf = %v", got)
	}
}

func TestValidDevice(t *testing.T) {
	for _, ok := range []string{"a", "k3J9xQ2mP0aZ7bWc", "A-Z_0-9", "abcdefghijklmnopqrstuvwxyz012345"} {
		if !ValidDevice(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "abcdefghijklmnopqrstuvwxyz0123456", "a:b", "a b", "é", "a/b", "a.b"} {
		if ValidDevice(bad) {
			t.Errorf("%q should not be valid", bad)
		}
	}
}
