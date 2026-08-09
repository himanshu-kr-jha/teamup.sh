import { Bell, BellOff, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { usePushSubscription } from './usePushSubscription';

/**
 * Turning on notifications that arrive when TeamUp is closed.
 *
 * Every way this can fail is a different sentence, because "notifications are
 * off" with a button that does nothing is the most annoying possible screen:
 * the browser may not do push at all, the site may not be on HTTPS, the server
 * may have no keys, or permission may have been refused — and only the last of
 * those is something a person can fix from here.
 */
export function PushSettings() {
  const { state, busy, enable, disable } = usePushSubscription();

  if (state === 'checking') {
    return (
      <Card>
        <CardHeader><CardTitle>Notifications</CardTitle></CardHeader>
        <CardContent>
          <Loader2 className="h-4 w-4 animate-spin" aria-label="Checking" />
        </CardContent>
      </Card>
    );
  }

  const explanation: Record<string, string> = {
    unsupported: 'This browser cannot receive notifications while TeamUp is closed.',
    insecure:
      'Notifications need a secure connection. On plain http they are unavailable — reach TeamUp over https and this will work.',
    unconfigured:
      'This server has no notification keys set up, so nothing can be delivered. An administrator can add them.',
    denied:
      'You refused notifications for this site. Browsers will not ask twice — allow them in the site settings beside the address bar, then come back.',
    off: 'Calls and messages will only reach you while TeamUp is open in a tab.',
    on: 'Calls and messages reach you on this device even when TeamUp is closed.',
  };

  return (
    <Card>
      <CardHeader><CardTitle>Notifications</CardTitle></CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-start gap-3">
          {state === 'on' ? (
            <Bell className="mt-0.5 h-5 w-5 shrink-0 text-primary" aria-hidden="true" />
          ) : (
            <BellOff className="mt-0.5 h-5 w-5 shrink-0 text-muted-foreground" aria-hidden="true" />
          )}
          <p className="text-sm text-muted-foreground">{explanation[state]}</p>
        </div>

        {state === 'off' && (
          <Button onClick={enable} disabled={busy}>
            {busy && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            Turn on notifications
          </Button>
        )}
        {state === 'on' && (
          <Button variant="outline" onClick={disable} disabled={busy}>
            {busy && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            Turn off on this device
          </Button>
        )}

        <p className="text-xs text-muted-foreground">
          This is per device. Turning it on here does not turn it on for your phone.
        </p>
      </CardContent>
    </Card>
  );
}
