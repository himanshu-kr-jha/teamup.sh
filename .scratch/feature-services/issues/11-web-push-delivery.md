# 11 — Web Push delivery

**What to build:** A notification reaches the user when the tab is closed.

The push sending path exists, but the device subscriptions it would send to are never stored or read, and the VAPID keypair the setup wizard generated is never loaded — the running service takes VAPID from environment variables instead, so an organization that completed setup normally has no working keys. Both halves of the keypair are already persisted with the organization; this slice makes them the source of truth.

**Blocked by:** 03 — Organization profile and configuration; 10 — In-app notification centre.

**Status:** ready-for-agent

- [ ] The browser can subscribe to push and the subscription is stored against the user and device
- [ ] Subscribing twice from the same device updates the existing subscription rather than duplicating it
- [ ] Unsubscribing removes the subscription and stops delivery to that device
- [ ] The VAPID keypair is read from the organization's stored configuration, with environment variables only as a fallback
- [ ] An in-app notification is also delivered as a push to the user's subscribed devices
- [ ] Clicking a push focuses an existing tab if one is open, otherwise opens the app at the relevant task or message
- [ ] A subscription rejected as expired or invalid by the push service is removed rather than retried forever
- [ ] The permission prompt is requested from a deliberate user action, not on page load
