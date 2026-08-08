# 18 — Fix calls package: LiveKit API imports and unused variables

**What to build:** Fix the calls package by updating LiveKit server-sdk-go imports to v2.7.0 API and removing unused variables.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Update LiveKit imports to use v2.7.0 API (livekit.ServerInfo, livekit.RoomServiceClient, etc.)
- [x] Fix livekit.CreateRoomRequest, livekit.RoomRecordingRequest struct usage
- [x] Update livekit.RecordingOutput_MP4, livekit.RecordingPreset_H264_720P_30 constants
- [x] Fix livekit.SignalRequest_Offer, SignalRequest_Answer, SignalRequest_Ice usage
- [x] Fix livekit.ICE struct field names (Candidate, SdpMid, SdpMlineindex)
- [x] Remove unused 'userID' variable in calls.go line 118
- [x] Add missing livekit import for ServerInfo if needed

**Type:** task
