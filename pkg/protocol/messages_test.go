package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/teamup/teamup/pkg/protocol"
)

// The envelope is used in both directions by the same struct, so decoding an
// inbound frame and marshalling an outbound one have to work off one field.
//
// Inbound is what was broken: the field was an empty interface, which
// encoding/json fills with map[string]interface{}, and every handler asserted
// it to []byte and returned when that failed. Holding the raw bytes is what
// lets a handler unmarshal into its own payload type.

func TestAnInboundFramesPayloadDecodesIntoItsOwnType(t *testing.T) {
	frame := []byte(`{"type":"chat.send","payload":{"channelId":"c-1","content":"hello","tempId":"t-1"}}`)

	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if msg.Type != protocol.TypeChatSend {
		t.Fatalf("type: got %q", msg.Type)
	}

	var payload protocol.ChatSendPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("a handler cannot decode the payload it was sent: %v", err)
	}
	if payload.ChannelID != "c-1" || payload.Content != "hello" || payload.TempID != "t-1" {
		t.Errorf("payload did not survive decoding: %+v", payload)
	}
}

// A frame with no payload at all must not stop the envelope decoding, since
// conn.ping and friends carry nothing.
func TestAFrameWithoutAPayloadStillDecodes(t *testing.T) {
	var msg protocol.Message
	if err := json.Unmarshal([]byte(`{"type":"conn.ping"}`), &msg); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if msg.Type != protocol.TypeConnPing {
		t.Errorf("type: got %q", msg.Type)
	}
}

// The same struct goes back out again, so the wire format the browser reads
// must be unchanged: a payload object, not a base64 string and not a nested
// wrapper.
func TestAnOutboundMessageMarshalsItsPayloadInline(t *testing.T) {
	msg := protocol.NewMessage(protocol.TypeChatTyping, protocol.ChatTypingPayload{
		ChannelID: "c-1",
		IsTyping:  true,
	})

	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}

	var onTheWire struct {
		Type    string `json:"type"`
		Payload struct {
			ChannelID string `json:"channelId"`
			IsTyping  bool   `json:"isTyping"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(encoded, &onTheWire); err != nil {
		t.Fatalf("a client cannot read what we sent (%s): %v", encoded, err)
	}
	if onTheWire.Type != protocol.TypeChatTyping {
		t.Errorf("type on the wire: got %q", onTheWire.Type)
	}
	if onTheWire.Payload.ChannelID != "c-1" || !onTheWire.Payload.IsTyping {
		t.Errorf("payload on the wire: got %+v from %s", onTheWire.Payload, encoded)
	}
}

// call.signaling carries a payload nested two deep and relays nothing
// observable, so the envelope is the only place its decoding can be pinned
// down.
func TestANestedSignalingPayloadDecodes(t *testing.T) {
	frame := []byte(`{"type":"call.signaling","payload":{"roomName":"call-1","targetParticipant":"u-2",` +
		`"payload":{"type":"ice-candidate","candidate":"candidate:1 1 udp","sdpMid":"0","sdpMLineIndex":3}}}`)

	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode frame: %v", err)
	}

	var payload protocol.CallSignalingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("a handler cannot decode the payload it was sent: %v", err)
	}
	if payload.RoomName != "call-1" || payload.TargetParticipant != "u-2" {
		t.Errorf("outer payload: %+v", payload)
	}
	if payload.Payload.Type != "ice-candidate" || payload.Payload.SDPMLineIndex != 3 {
		t.Errorf("inner signalling payload: %+v", payload.Payload)
	}
}

// Round-tripping is what the relay paths do: a frame comes in, is decoded, and
// the decoded payload goes back out to somebody else.
func TestAPayloadSurvivesAFullRoundTrip(t *testing.T) {
	incoming := []byte(`{"type":"chat.typing","payload":{"channelId":"c-9","isTyping":true}}`)

	var msg protocol.Message
	if err := json.Unmarshal(incoming, &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var payload protocol.ChatTypingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	encoded, err := json.Marshal(protocol.NewMessage(protocol.TypeChatTyping, payload))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var again protocol.Message
	if err := json.Unmarshal(encoded, &again); err != nil {
		t.Fatalf("decode again: %v", err)
	}
	var back protocol.ChatTypingPayload
	if err := json.Unmarshal(again.Payload, &back); err != nil {
		t.Fatalf("decode payload again: %v", err)
	}
	if back != payload {
		t.Errorf("round trip changed the payload: %+v -> %+v", payload, back)
	}
}
