# 16 — Fix notifications package: webpush-go v2 API compatibility

**What to build:** Update the notifications package to be compatible with webpush-go v2 API (VAPID struct, IsSubscriptionExpired/IsSubscriptionGone functions).

**Blocked by:** None — can start immediately

**Status:** resolved

- [x] Update VAPID struct usage to match webpush-go v2 API
- [x] Replace webpush.IsSubscriptionExpired with correct v2 function
- [x] Replace webpush.IsSubscriptionGone with correct v2 function
- [x] Fix webpush.SendNotification call signature for v2
- [x] Update VAPID key generation/storage to match v2 expectations
- [x] Remove unused 'id' variable in routes.go (line 70)
- [x] Remove unused 'user' variable in routes.go (line 109)

**Type:** task
