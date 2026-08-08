# 17 — Fix presence package: msg.Payload type assertion

**What to build:** Add type assertion for msg.Payload in HandlePresenceUpdate to fix the json.Unmarshal error.

**Blocked by:** 14 — Fix chat package (WSConnection interface must be defined first)

**Status:** resolved

- [x] Add type assertion: payloadBytes, ok := msg.Payload.([]byte) before json.Unmarshal
- [x] Handle case where payload is not []byte (log error, return early)
- [x] Ensure json import is present
- [x] Verify WSConnection interface is imported from websocket package

**Type:** task
