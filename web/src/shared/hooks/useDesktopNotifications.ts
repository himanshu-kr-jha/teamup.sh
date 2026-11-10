import { useCallback, useEffect, useState } from 'react';
import { pushIsOn } from '@/features/notifications/pushState';

type Permission = 'default' | 'granted' | 'denied' | 'unsupported';

interface Notify {
  title: string;
  body: string;
  /** Replaces an earlier notification with the same tag rather than stacking. */
  tag?: string;
  /** Where clicking it goes, when the notification outlives this page. */
  url?: string;
  onClick?: () => void;
  /**
   * The server sends this one to the device as a push too. Then this page
   * leaves it to the push whenever it would be shown anyway, so it does not
   * arrive twice (see push-sw.js).
   */
  alsoPushed?: boolean;
}

/**
 * Whether the person has TeamUp in front of them. The service worker asks the
 * same question of the windows it knows about (push-sw.js), and stays quiet
 * when the answer is yes; the page notifies in exactly the other case.
 */
function inFront() {
  return document.visibilityState === 'visible' && document.hasFocus();
}

/**
 * Desktop notifications, for while the site is open.
 *
 * This is the Notification API, not web push: it reaches somebody who has
 * TeamUp open in a tab they are not looking at. Delivery to a closed browser,
 * or to a phone, is a push subscription's.
 */
export function useDesktopNotifications() {
  const [permission, setPermission] = useState<Permission>(() =>
    typeof Notification === 'undefined' ? 'unsupported' : (Notification.permission as Permission)
  );

  // Permission can be changed from browser settings while the page is open.
  useEffect(() => {
    if (typeof Notification === 'undefined') return;
    const sync = () => setPermission(Notification.permission as Permission);
    document.addEventListener('visibilitychange', sync);
    return () => document.removeEventListener('visibilitychange', sync);
  }, []);

  /**
   * Asks for permission. Must be called from something the user did: browsers
   * ignore, and some penalise, a request made on page load.
   */
  const request = useCallback(async () => {
    if (typeof Notification === 'undefined') return 'unsupported' as const;
    const result = await Notification.requestPermission();
    setPermission(result as Permission);
    return result;
  }, []);

  const notify = useCallback(({ title, body, tag, url, onClick, alsoPushed }: Notify) => {
    if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
    if (alsoPushed && pushIsOn() && !inFront()) return;
    const options = {
      body,
      tag,
      icon: '/icons/icon-192.png',
      // Replacing silently would let a burst of messages arrive unnoticed.
      renotify: !!tag,
    } as NotificationOptions;
    try {
      const notification = new Notification(title, options);
      notification.onclick = () => {
        window.focus();
        notification.close();
        onClick?.();
      };
    } catch {
      // Chrome on Android has no notifications but a service worker's: the
      // constructor throws there. The worker's click handler takes it to url.
      if (!('serviceWorker' in navigator)) return;
      void navigator.serviceWorker.ready
        .then((registration) => registration.showNotification(title, { ...options, data: { url: url ?? window.location.pathname } }))
        .catch(() => {
          // Losing the notification is not worth interrupting anything for.
        });
    }
  }, []);

  return { permission, request, notify };
}
