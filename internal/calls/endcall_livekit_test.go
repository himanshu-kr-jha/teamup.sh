package calls

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// Ending a call has to close its room, or whoever is in it stays connected to a
// call that is over. LiveKit deletes a room by its name; given the sid it answers
// not_found, so this needs the real server to mean anything.
func TestEndCallReallyClosesTheRoomOnLiveKit(t *testing.T) {
	url := os.Getenv("TEAMUP_TEST_LIVEKIT_URL")
	if url == "" {
		t.Skip("TEAMUP_TEST_LIVEKIT_URL not set")
	}
	httpURL := strings.Replace(url, "ws://", "http://", 1)
	secret := os.Getenv("TEAMUP_TEST_LIVEKIT_SECRET")
	if secret == "" {
		secret = "devsecret"
	}
	rooms := lksdk.NewRoomServiceClient(httpURL, "devkey", secret)
	svc := NewService(nil, Config{LiveKitURL: httpURL, LiveKitAPIKey: "devkey", LiveKitAPISecret: secret})

	ctx := context.Background()
	name := "end-call-test-" + uuid.NewString()[:8]
	if _, err := rooms.CreateRoom(ctx, &livekit.CreateRoomRequest{Name: name}); err != nil {
		t.Fatalf("create room: %v", err)
	}
	if err := svc.EndCall(ctx, name); err != nil {
		t.Errorf("end call: %v", err)
	}
	left, err := rooms.ListRooms(ctx, &livekit.ListRoomsRequest{Names: []string{name}})
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	if len(left.Rooms) != 0 {
		t.Errorf("the room should be gone, still there: %v", left.Rooms)
	}
}
