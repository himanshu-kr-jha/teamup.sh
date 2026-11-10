/*
 * Push handling, imported into the generated service worker.
 *
 * This is the half that runs when nothing is open: the browser wakes the
 * worker, hands it the payload, and it has a few moments to put a notification
 * on the screen. Everything in here therefore has to work with no page, no
 * React, and no session — only what the payload carries.
 *
 * Plain JavaScript in `public/` rather than a module in `src/`, so that the
 * service worker Workbox generates can `importScripts` it. Building our own
 * worker instead would mean reimplementing the precaching and the runtime
 * caching rules that come with the generated one.
 */

/* global self, clients */

self.addEventListener('push', (event) => {
  if (!event.data) return;

  let payload;
  try {
    payload = event.data.json();
  } catch {
    payload = { title: 'TeamUp', body: event.data.text() };
  }

  const data = payload.data || {};

  // A cancelled call should stop ringing rather than sit there until it is
  // dismissed by hand, so this arrives as a push whose whole job is to take
  // the earlier one away.
  if (data.kind === 'call.cancelled') {
    event.waitUntil(
      self.registration.getNotifications({ tag: payload.tag }).then((existing) => {
        existing.forEach((n) => n.close());
      })
    );
    return;
  }

  event.waitUntil(
    (async () => {
      // Somebody looking at the app does not need to be told what is on their
      // screen. The page raises its own, quieter notice for a window that is
      // merely open behind something else.
      const windows = await clients.matchAll({ type: 'window', includeUncontrolled: true });
      const looking = windows.some((c) => c.visibilityState === 'visible' && c.focused);
      if (looking && data.kind !== 'call.incoming') return;

      const isCall = data.kind === 'call.incoming';
      // A call rings by being put up again every few seconds (keepRinging on
      // the server). In front of the person, the call's own dialog is already
      // ringing, so once it is up it is not sounded over that again.
      if (isCall && looking && payload.tag) {
        const up = await self.registration.getNotifications({ tag: payload.tag });
        if (up.length > 0) return;
      }
      await self.registration.showNotification(payload.title || 'TeamUp', {
        body: payload.body || '',
        icon: payload.icon || '/icons/icon-192.png',
        badge: payload.badge || '/icons/icon-192.png',
        tag: payload.tag,
        // Without this a second message with the same tag replaces the first
        // in silence, so a burst arrives as one soundless update.
        renotify: !!payload.tag,
        // A call waits to be answered; a message does not hold the screen.
        requireInteraction: isCall || !!payload.requireInteraction,
        silent: !!payload.silent,
        vibrate: isCall ? [400, 200, 400, 200, 400] : [120],
        data,
        actions: payload.actions || [],
      });
    })()
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();

  const data = event.notification.data || {};
  const target = data.url || '/';

  event.waitUntil(
    (async () => {
      const windows = await clients.matchAll({ type: 'window', includeUncontrolled: true });

      // Reusing the window that is already open, rather than opening a second
      // copy of the app beside it — and taking it to the conversation or call
      // the notification was about.
      for (const client of windows) {
        if ('focus' in client) {
          await client.focus();
          if ('navigate' in client) {
            try {
              await client.navigate(target);
            } catch {
              // Cross-origin or a client that refuses; the focus still helped.
            }
          }
          return;
        }
      }
      if (clients.openWindow) await clients.openWindow(target);
    })()
  );
});
