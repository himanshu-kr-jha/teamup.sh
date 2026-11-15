import { useEffect } from 'react';
import { presenceApi } from '@/shared/api/client';

/**
 * Tells the server which time zone this browser is in, once per page load, so a
 * call or a stand-up ring knows whether it is your leave day or your afternoon
 * off. Best effort: without it the server reads your day in UTC. Not while a
 * new password is still owed, when the server refuses everything else.
 */
export function useKeepTimeZone(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    let zone: string | undefined;
    try {
      zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    } catch {
      return;
    }
    if (zone) presenceApi.setZone(zone).catch(() => {});
  }, [enabled]);
}
