# 26 — Fix main.go: PresenceServiceInterface implementation mismatch

**What to build:** Fix the PresenceService to implement the websocket.PresenceServiceInterface exactly, including the GetAllOnline method signature.

**Blocked by:** 25 — Fix main.go: Config struct mismatch

**Status:** completed

- [x] Update presence.Service.GetAllOnline to accept string parameter instead of uuid.UUID
- [x] Update presence.Service.GetAllOnline to return []websocket.Presence instead of []presence.Presence
- [x] Add GetPresence method matching the interface signature
- [x] Update presence.Presence struct to match websocket.Presence fields

**Type:** task
