import { useCallback, useEffect, useState } from 'react';

type Permission = 'default' | 'granted' | 'denied' | 'unsupported';

interface Notify {
  title: string;
  body: string;
  /** Replaces an earlier notification with the same tag rather than stacking. */
  tag?: string;
  onClick?: () => void;
}

/**
 * Desktop notifications, for while the site is open.
 *
 * This is the Notification API, not web push: it reaches somebody who has
 * TeamUp open in a tab they are not looking at, which is the common case.
 * Delivery to a closed browser is a separate thing and needs a push
 * subscription.
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

  const notify = useCallback(
    ({ title, body, tag, onClick }: Notify) => {
      if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
      try {
        const notification = new Notification(title, {
          body,
          tag,
          icon: '/icons/icon-192.png',
          // Replacing silently would let a burst of messages arrive unnoticed.
          renotify: !!tag,
        } as NotificationOptions);
        notification.onclick = () => {
          window.focus();
          notification.close();
          onClick?.();
        };
      } catch {
        // Some browsers refuse to construct one outside a service worker.
        // Losing the notification is not worth interrupting anything for.
      }
    },
    []
  );

  return { permission, request, notify };
}
