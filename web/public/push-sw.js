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

/* global clients */

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

// Where a notification may take somebody: a page of this app, or a pull
// request or commit on GitHub or Bitbucket. Anything else opens the app.
function safeTarget(url) {
  if (!url) return '/';
  try {
    const u = new URL(url, self.location.origin);
    if (u.origin === self.location.origin) return u.pathname + u.search + u.hash;
    if (u.protocol === 'https:' && (u.hostname === 'github.com' || u.hostname === 'bitbucket.org')) return u.href;
  } catch {
    // Not an address at all.
  }
  return '/';
}

self.addEventListener('notificationclick', (event) => {
  event.notification.close();

  const data = event.notification.data || {};
  const target = safeTarget(data.url);

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

/*
 * Sharing into TeamUp from another app.
 *
 * The manifest's share_target makes TeamUp one of the apps a phone's share
 * sheet offers. What is shared arrives as a form POSTed to /share, files and
 * all, which only the worker can receive: it keeps them in a cache the page
 * reads (src/features/share/inbox.ts) and sends the window on to /share, where
 * the person chooses whom to send them to. The names here and there must match.
 */
const SHARE_INBOX = 'teamup-share-inbox';
/** What one share may keep. More than this is left out, and the page says so. */
const SHARE_MAX_FILES = 10;
const SHARE_MAX_BYTES = 100 * 1024 * 1024;

// A phone's share sheet posts with no referrer. A page elsewhere on the web can
// post a form to /share too, and would come with its own address: that is not
// somebody sharing, it is a site planting files and words for them to send.
function postedFromElsewhere(request) {
  const from = request.referrer;
  if (!from || from === 'about:client') return false;
  try {
    return new URL(from).origin !== self.location.origin;
  } catch {
    return true;
  }
}

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'POST' || url.origin !== self.location.origin || url.pathname !== '/share') return;

  if (postedFromElsewhere(event.request)) {
    // Nothing is read or kept; the page shows whatever was waiting before.
    event.respondWith(Response.redirect('/share', 303));
    return;
  }

  event.respondWith(
    (async () => {
      try {
        const form = await event.request.formData();
        // Whatever came last time and was never sent is replaced, not added to.
        await caches.delete(SHARE_INBOX);
        const inbox = await caches.open(SHARE_INBOX);
        const files = [];
        let bytes = 0;
        let leftOut = 0;
        for (const file of form.getAll('files')) {
          if (!(file instanceof File) || file.size === 0) continue;
          if (files.length >= SHARE_MAX_FILES || bytes + file.size > SHARE_MAX_BYTES) {
            leftOut++;
            continue;
          }
          bytes += file.size;
          const key = `/share-inbox/file/${files.length}`;
          files.push({ key, name: file.name || 'file', type: file.type || 'application/octet-stream', size: file.size });
          await inbox.put(key, new Response(file, { headers: { 'Content-Type': file.type || 'application/octet-stream' } }));
        }
        const text = (name) => {
          const v = form.get(name);
          return typeof v === 'string' ? v : '';
        };
        await inbox.put(
          '/share-inbox/meta',
          new Response(
            JSON.stringify({ title: text('title'), text: text('text'), url: text('url'), files, leftOut, at: Date.now() }),
            { headers: { 'Content-Type': 'application/json' } }
          )
        );
      } catch {
        // The page says nothing arrived, which is true; there is no one else to tell.
      }
      return Response.redirect('/share', 303);
    })()
  );
});
