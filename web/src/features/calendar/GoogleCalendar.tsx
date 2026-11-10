import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, Loader2, RefreshCw, Unplug } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { calendarApi, type GoogleCalendarStatus } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { formatRelativeTime } from '@/shared/utils/cn';

/** Google's four colours, small, so the button says whose calendar it is without a logo. */
function GoogleMark() {
  return (
    <span className="mr-1.5 grid h-3.5 w-3.5 grid-cols-2 gap-px overflow-hidden rounded-sm" aria-hidden="true">
      <span className="bg-[#4285F4]" />
      <span className="bg-[#EA4335]" />
      <span className="bg-[#34A853]" />
      <span className="bg-[#FBBC05]" />
    </span>
  );
}

const OUTCOMES: Record<string, { ok: boolean; text: string }> = {
  connected: { ok: true, text: 'Google Calendar connected. Your meetings are on their way in.' },
  declined: { ok: false, text: 'Google Calendar was not connected: access was not allowed on Google’s page.' },
  failed: { ok: false, text: 'Google Calendar could not be connected. Please try again.' },
};

/**
 * Connecting your Google Calendar, and how it is going once connected.
 *
 * Meetings in Google Calendar show here as busy time, so nobody books over
 * them; TeamUp meetings you are in go into Google Calendar, and move or leave
 * with them. Nothing is shown until an administrator has set Google up.
 */
export function GoogleCalendarButton({ outcome, onOutcomeSeen }: { outcome: string | null; onOutcomeSeen: () => void }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<'connect' | 'sync' | 'disconnect' | null>(null);
  const [confirmingDisconnect, setConfirmingDisconnect] = useState(false);
  const { data } = useQuery({ queryKey: ['google-calendar'], queryFn: () => calendarApi.google().then((r) => r.data) });

  // Back from Google's consent page, with how it went in the address.
  useEffect(() => {
    if (!outcome) return;
    const said = OUTCOMES[outcome];
    if (said) (said.ok ? toast.success : toast.error)(said.text);
    void queryClient.invalidateQueries({ queryKey: ['google-calendar'] });
    void queryClient.invalidateQueries({ queryKey: ['calendar'] });
    onOutcomeSeen();
    // toast is rebuilt each render; the outcome is what this answers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outcome]);

  if (!data?.available) return null;

  const connect = async () => {
    setBusy('connect');
    try {
      const { data: res } = await calendarApi.connectGoogle();
      window.location.assign(res.url);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not reach Google. Please try again.'));
      setBusy(null);
    }
  };

  const sync = async () => {
    setBusy('sync');
    try {
      const { data: st } = await calendarApi.syncGoogle();
      queryClient.setQueryData(['google-calendar'], st);
      await queryClient.invalidateQueries({ queryKey: ['calendar'] });
      if (st.lastError) toast.error(st.lastError);
      else toast.success('Up to date with Google Calendar.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not reach Google Calendar.'));
    } finally {
      setBusy(null);
    }
  };

  const disconnect = async () => {
    setBusy('disconnect');
    try {
      await calendarApi.disconnectGoogle();
      queryClient.setQueryData<GoogleCalendarStatus>(['google-calendar'], { available: true, connected: false });
      await queryClient.invalidateQueries({ queryKey: ['calendar'] });
      toast.success('Google Calendar disconnected.');
      setOpen(false);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not disconnect Google Calendar.'));
    } finally {
      setBusy(null);
      setConfirmingDisconnect(false);
    }
  };

  if (!data.connected) {
    return (
      <Button variant="outline" size="sm" className="h-9" onClick={connect} disabled={busy !== null}>
        {busy === 'connect' ? <Loader2 className="mr-1.5 h-4 w-4 animate-spin" aria-hidden="true" /> : <GoogleMark />}
        Connect Google Calendar
      </Button>
    );
  }

  return (
    <>
      <Button variant="outline" size="sm" className="h-9" onClick={() => setOpen(true)} aria-label="Google Calendar settings">
        <GoogleMark />
        Google Calendar
        {data.needsReconnect || data.lastError ? (
          <AlertTriangle className="ml-1.5 h-4 w-4 text-amber-500" aria-label="Needs attention" />
        ) : (
          <CheckCircle2 className="ml-1.5 h-4 w-4 text-emerald-600" aria-label="Connected" />
        )}
      </Button>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) setConfirmingDisconnect(false);
        }}
      >
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Google Calendar</DialogTitle>
            <DialogDescription>
              Connected{data.email ? ` as ${data.email}` : ''}.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 text-sm">
            <ul className="list-disc space-y-1 pl-5 text-muted-foreground">
              <li>Meetings in your Google Calendar show here as busy time, so nobody books over them.</li>
              <li>TeamUp meetings you are in are added to your Google Calendar, and move or go with them.</li>
              <li>Your manager can see the names of your Google meetings, except ones marked private there.</li>
            </ul>
            {data.needsReconnect ? (
              <p role="alert" className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-50 p-2 text-amber-950 dark:bg-amber-950/40 dark:text-amber-100">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                Google no longer lets TeamUp in. Connect again to carry on.
              </p>
            ) : data.lastError ? (
              <p role="alert" className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-50 p-2 text-amber-950 dark:bg-amber-950/40 dark:text-amber-100">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                {data.lastError}
              </p>
            ) : (
              <p className="text-muted-foreground">
                {data.lastSyncedAt ? `Last brought up to date ${formatRelativeTime(data.lastSyncedAt)}.` : 'Bringing your calendars up to date…'}{' '}
                It happens every few minutes, and as soon as a meeting changes here.
              </p>
            )}
          </div>
          {confirmingDisconnect ? (
            <DialogFooter className="gap-2">
              <p className="mr-auto self-center text-sm">Take TeamUp out of your Google Calendar?</p>
              <Button variant="outline" disabled={busy !== null} onClick={() => setConfirmingDisconnect(false)}>
                Keep it
              </Button>
              <Button variant="destructive" disabled={busy !== null} onClick={disconnect}>
                {busy === 'disconnect' && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
                Disconnect
              </Button>
            </DialogFooter>
          ) : (
            <DialogFooter className="flex-row flex-wrap justify-end gap-2 sm:space-x-0">
              <Button variant="outline" disabled={busy !== null} onClick={() => setConfirmingDisconnect(true)}>
                <Unplug className="mr-1 h-4 w-4" aria-hidden="true" />
                Disconnect
              </Button>
              {data.needsReconnect ? (
                <Button disabled={busy !== null} onClick={connect}>
                  {busy === 'connect' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" /> : <GoogleMark />}
                  Connect again
                </Button>
              ) : (
                <Button disabled={busy !== null} onClick={sync}>
                  {busy === 'sync' ? (
                    <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" />
                  ) : (
                    <RefreshCw className="mr-1 h-4 w-4" aria-hidden="true" />
                  )}
                  Sync now
                </Button>
              )}
            </DialogFooter>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
