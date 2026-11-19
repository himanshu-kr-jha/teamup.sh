// Package participant reads and writes who somebody is in a LiveKit room.
//
// A room knows each connection by an identity, and TeamUp gives out three kinds:
//
//   - a member: "<userId>:<deviceId>", one per browser they are in the call from,
//     so a laptop and a phone can both be in at once (companion mode). Before
//     companion mode a member's identity was the bare "<userId>", and a token
//     minted that way, or by an older page, still reads as that member.
//   - a guest let in by a meeting's link: "guest-<guestId>".
//   - the note-taker: "notes-<callId>". It is not a person.
//
// Everything that counts people, rings them, or puts their hand up goes by the
// person (Person), never by the connection, so somebody in from two devices is
// still one person. Only the media server's own questions (this connection,
// this track) go by the identity.
//
// It has no dependencies of its own, so internal/calls and internal/notetaker
// can share it.
package participant

import (
	"strings"

	"github.com/google/uuid"
)

const (
	// GuestPrefix starts a guest's identity.
	GuestPrefix = "guest-"
	// NoteTakerPrefix starts the note-taker's identity.
	NoteTakerPrefix = "notes-"
	// deviceSeparator separates a member's user id from their device.
	deviceSeparator = ":"
	// DeviceAttribute is the participant attribute that says what kind of
	// device a member's connection is from (DeviceKinds), set in their token.
	DeviceAttribute = "device"
	// maxDeviceID bounds a device id. The browser makes one of 16 characters.
	maxDeviceID = 32
)

// DeviceKinds are the kinds of device a tile can be labelled with.
var DeviceKinds = map[string]bool{"phone": true, "tablet": true, "laptop": true}

// Kind is what sort of participant an identity is.
type Kind int

const (
	// Unknown is not an identity TeamUp gave out: an egress, or a room made by hand.
	Unknown Kind = iota
	// Member is a person in the organization, from one of their devices.
	Member
	// Guest is somebody from outside, let in by a meeting's link.
	Guest
	// NoteTaker is the bot that takes a meeting's minutes.
	NoteTaker
)

// Identity is an identity read back.
type Identity struct {
	Kind Kind
	// UserID is a member's user id, or a guest's guest id.
	UserID uuid.UUID
	// Device is a member's device id, empty for one who joined without one.
	Device string
}

// ValidDevice says whether a device id is one a browser could have made: short,
// and letters, digits, '-' and '_' only, so it can never carry a separator.
func ValidDevice(device string) bool {
	if device == "" || len(device) > maxDeviceID {
		return false
	}
	for _, r := range device {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// MemberIdentity is a member's identity in a room from one device. Without a
// device, as an older page asks, it is their bare user id.
func MemberIdentity(userID string, device string) string {
	if device == "" || !ValidDevice(device) {
		return userID
	}
	return userID + deviceSeparator + device
}

// Parse reads an identity. Anything not given out by TeamUp is Unknown.
func Parse(identity string) Identity {
	switch {
	case strings.HasPrefix(identity, NoteTakerPrefix):
		return Identity{Kind: NoteTaker}
	case strings.HasPrefix(identity, GuestPrefix):
		id, err := uuid.Parse(strings.TrimPrefix(identity, GuestPrefix))
		if err != nil {
			return Identity{}
		}
		return Identity{Kind: Guest, UserID: id}
	}
	user, device, hasDevice := strings.Cut(identity, deviceSeparator)
	if hasDevice && !ValidDevice(device) {
		return Identity{}
	}
	// uuid.Parse also takes braces and a "urn:uuid:" prefix; only the plain
	// 36-character form is ours.
	if len(user) != 36 {
		return Identity{}
	}
	id, err := uuid.Parse(user)
	if err != nil {
		return Identity{}
	}
	return Identity{Kind: Member, UserID: id, Device: device}
}

// Person is who an identity belongs to, the same for each of somebody's
// devices: the user id of a member, the whole identity of a guest, and nothing
// for the note-taker or an identity that is not ours.
func Person(identity string) string {
	p := Parse(identity)
	switch p.Kind {
	case Member:
		return p.UserID.String()
	case Guest:
		return GuestPrefix + p.UserID.String()
	}
	return ""
}

// IsMember says whether an identity is this member's, from any device.
func IsMember(identity string, userID uuid.UUID) bool {
	p := Parse(identity)
	return p.Kind == Member && p.UserID == userID
}

// CountPeople is how many people the connected identities are: each person once
// however many devices they are in from, and not the note-taker.
func CountPeople(identities []string) int {
	seen := map[string]bool{}
	for _, id := range identities {
		if who := Person(id); who != "" {
			seen[who] = true
		}
	}
	return len(seen)
}

// AnybodyBut says whether any connection other than this one is still in the
// room, the note-taker apart. Another device of the same person counts: a call
// does not end while any device of anyone is in it.
func AnybodyBut(identities []string, me string) bool {
	for _, id := range identities {
		if id == me || Parse(id).Kind == NoteTaker {
			continue
		}
		return true
	}
	return false
}

// DevicesOf is this member's connections among the identities, from any device.
func DevicesOf(identities []string, userID uuid.UUID) []string {
	var out []string
	for _, id := range identities {
		if IsMember(id, userID) {
			out = append(out, id)
		}
	}
	return out
}
