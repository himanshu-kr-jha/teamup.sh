import { useCallback, useEffect, useState } from 'react';
import { notificationsApi } from '@/shared/api/client';
import { setPushOn } from './pushState';

export type PushState =
  | 'unsupported' // no service worker or no push in this browser
  | 'insecure' // not HTTPS, so the browser will not allow it
  | 'unconfigured' // the server has no VAPID keys
  | 'checking'
  | 'off'
  | 'denied'
  | 'on';

/**
 * A push key arrives base64url-encoded and has to be handed over as bytes.
 */
function decodeKey(base64: string): ArrayBuffer {
  const padded = (base64 + '='.repeat((4 - (base64.length % 4)) % 4))
    .replace(/-/g, '+')
    .replace(/_/g, '/');
  const raw = atob(padded);
  // An ArrayBuffer rather than a view over one: a Uint8Array may sit on shared
  // memory, which is not what subscribe accepts.
  const buffer = new ArrayBuffer(raw.length);
  const bytes = new Uint8Array(buffer);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return buffer;
}

/** What the subscription hands over, as the API wants it. */
function keysOf(sub: PushSubscription) {
  const json = sub.toJSON();
  return {
    endpoint: sub.endpoint,
    p256dh: json.keys?.p256dh ?? '',
    auth: json.keys?.auth ?? '',
  };
}

/**
 * Registers this device for notifications that arrive when nothing is open.
 *
 * The Notification API alone only reaches a tab that still exists; this is the
 * part that reaches a closed browser or a phone in a pocket, which is what
 * makes a call worth ringing at all. It needs three things the browser will not
 * give up quietly: a secure origin, permission, and a key from the server.
 *
 * Permission has to be asked for from something the person did — browsers
 * ignore, and some penalise, a request made on load. So enabling is a button.
 * Re-subscribing when permission has already been given is not, and happens on
 * its own, because a subscription can be revoked by the browser at any time and
 * silently going deaf is the worst of the failure modes.
 */
export function usePushSubscription() {
  const [state, setStateOnly] = useState<PushState>('checking');
  const setState = useCallback((next: PushState) => {
    setPushOn(next === 'on');
    setStateOnly(next);
  }, []);
  const [busy, setBusy] = useState(false);

  const supported =
    typeof navigator !== 'undefined' &&
    'serviceWorker' in navigator &&
    typeof PushManager !== 'undefined' &&
    typeof Notification !== 'undefined';

  const subscribe = useCallback(async (): Promise<PushState> => {
    const { data } = await notificationsApi.vapidKey();
    if (!data?.enabled || !data?.publicKey) return 'unconfigured';

    const registration = await navigator.serviceWorker.ready;
    // An existing subscription is reused rather than replaced: the browser
    // keeps one per registration, and asking for another with a different key
    // fails outright.
    let sub = await registration.pushManager.getSubscription();
    if (!sub) {
      sub = await registration.pushManager.subscribe({
        // Without this Chrome refuses to subscribe at all, and it means every
        // push must carry a payload — which ours do.
        userVisibleOnly: true,
        applicationServerKey: decodeKey(data.publicKey),
      });
    }

    // Told to the server every time, not only when newly created: the row is
    // what delivery reads, and it can be lost while the browser's copy lives on.
    await notificationsApi.subscribe({
      ...keysOf(sub),
      deviceName: navigator.userAgent.slice(0, 200),
    });
    return 'on';
  }, []);

  // Work out where things stand, and quietly re-register a device that has
  // already agreed to this.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      if (!supported) return setState('unsupported');
      if (!window.isSecureContext) return setState('insecure');
      if (Notification.permission === 'denied') return setState('denied');
      if (Notification.permission !== 'granted') return setState('off');

      try {
        const result = await subscribe();
        if (!cancelled) setState(result);
      } catch {
        if (!cancelled) setState('off');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [supported, subscribe, setState]);

  /** Asks for permission and registers. Call this from a click. */
  const enable = useCallback(async () => {
    setBusy(true);
    try {
      const permission = await Notification.requestPermission();
      if (permission === 'denied') return setState('denied');
      if (permission !== 'granted') return setState('off');
      setState(await subscribe());
    } catch {
      setState('off');
    } finally {
      setBusy(false);
    }
  }, [subscribe, setState]);

  /** Stops this device receiving them. Permission is the browser's to revoke. */
  const disable = useCallback(async () => {
    setBusy(true);
    try {
      const registration = await navigator.serviceWorker.ready;
      const sub = await registration.pushManager.getSubscription();
      if (sub) {
        // The server is told first: a subscription dropped locally but left in
        // the database is one the server keeps writing to forever.
        await notificationsApi.unsubscribe(sub.endpoint).catch(() => {});
        await sub.unsubscribe();
      }
      setState('off');
    } finally {
      setBusy(false);
    }
  }, [setState]);

  return { state, busy, enable, disable };
}
