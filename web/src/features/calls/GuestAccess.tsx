import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, KeyRound, Link2, Loader2, RefreshCw, UserPlus } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { callsApi, type GuestLink } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { copyText } from './copyLink';

/** Six digits: easy to read out on the phone, and the server limits guessing. */
function newPasscode(): string {
  const n = new Uint32Array(1);
  crypto.getRandomValues(n);
  return String(n[0] % 1_000_000).padStart(6, '0');
}

export function guestURL(link: GuestLink): string {
  return `${window.location.origin}${link.path ?? ''}`;
}

/** The invitation to paste into an email or a message. */
export function invitation(link: GuestLink, title?: string): string {
  const lines = [`Join ${title ? `"${title}"` : 'the meeting'} as a guest:`, guestURL(link)];
  if (link.passcode) lines.push(`Passcode: ${link.passcode}`);
  return lines.join('\n');
}

/**
 * A meeting's guest link: people from outside the organization join with it,
 * under their own name and with the passcode if there is one. The organizer
 * and administrators turn it on and off; everybody in the meeting can pass it
 * on.
 */
export function GuestAccess({ callId, title }: { callId: string; title?: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data: link, isPending, error } = useQuery({
    queryKey: ['guest-link', callId],
    queryFn: async () => (await callsApi.guestLink(callId)).data,
  });
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [passcode, setPasscode] = useState('');

  const change = async (data: { enabled: boolean; passcode?: string; newLink?: boolean }, done: string) => {
    setBusy(true);
    try {
      const res = await callsApi.setGuestLink(callId, data);
      queryClient.setQueryData(['guest-link', callId], res.data);
      setEditing(false);
      toast.success(done);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not change the guest link.'));
    } finally {
      setBusy(false);
    }
  };

  const copy = async (text: string, what: string) => {
    if (await copyText(text)) toast.success(`${what} copied.`);
    else toast.error('Could not copy. Your browser did not allow it.');
  };

  if (isPending) return <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-label="Loading" />;
  if (error || !link) return null;

  if (!link.enabled) {
    return (
      <section aria-label="Guests" className="space-y-2 rounded-lg border border-border p-3 text-sm">
        <p className="flex items-center gap-2 font-medium">
          <UserPlus className="h-4 w-4 shrink-0" aria-hidden="true" />
          Guests
        </p>
        {link.canManage ? (
          <>
            <p className="text-muted-foreground">
              Let people from outside the organization join with a link. They give their name and a passcode, and come in once the meeting has started.
            </p>
            <Button size="sm" disabled={busy} onClick={() => change({ enabled: true, passcode: newPasscode() }, 'Guest link made.')}>
              {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Link2 className="mr-2 h-4 w-4" />}
              Make a guest link
            </Button>
          </>
        ) : (
          <p className="text-muted-foreground">Only the organizer can let guests in.</p>
        )}
      </section>
    );
  }

  return (
    <section aria-label="Guests" className="space-y-3 rounded-lg border border-border p-3 text-sm">
      <p className="flex items-center gap-2 font-medium">
        <UserPlus className="h-4 w-4 shrink-0" aria-hidden="true" />
        Guests can join with this link
      </p>
      <div className="flex min-w-0 items-center gap-2">
        <Input readOnly value={guestURL(link)} aria-label="Guest link" className="h-8 min-w-0 flex-1 font-mono text-xs" onFocus={(e) => e.target.select()} />
        <Button size="sm" variant="outline" className="h-8 shrink-0" onClick={() => copy(guestURL(link), 'Link')} aria-label="Copy the guest link">
          <Copy className="h-4 w-4" />
        </Button>
      </div>
      <p className="flex items-center gap-2">
        <KeyRound className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        {link.passcode ? (
          <>
            <span className="text-muted-foreground">Passcode</span>
            <span className="font-mono font-medium tracking-wider" data-testid="guest-passcode">
              {link.passcode}
            </span>
          </>
        ) : (
          <span className="text-muted-foreground">No passcode: anybody with the link can join.</span>
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={() => copy(invitation(link, title), 'Invitation')}>
          <Copy className="mr-2 h-4 w-4" />
          Copy invitation
        </Button>
        {link.canManage && !editing && (
          <>
            <Button size="sm" variant="outline" disabled={busy} onClick={() => { setPasscode(link.passcode ?? ''); setEditing(true); }}>
              Passcode
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() => change({ enabled: true, newLink: true }, 'New link made. The old one no longer works.')}
              title="The old link stops working; guests already in stay"
            >
              <RefreshCw className="mr-2 h-4 w-4" />
              New link
            </Button>
            <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" disabled={busy} onClick={() => change({ enabled: false }, 'Guest link turned off. Any guests have been taken out.')}>
              Turn off
            </Button>
          </>
        )}
      </div>
      {editing && (
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault();
            void change({ enabled: true, passcode: passcode.trim() }, passcode.trim() ? 'Passcode changed.' : 'Passcode removed.');
          }}
        >
          <Label htmlFor={`passcode-${callId}`}>Passcode, or empty for none</Label>
          <div className="flex gap-2">
            <Input
              id={`passcode-${callId}`}
              value={passcode}
              onChange={(e) => setPasscode(e.target.value)}
              maxLength={32}
              autoComplete="off"
              className="h-8 min-w-0 flex-1 font-mono"
            />
            <Button type="button" size="sm" variant="outline" className="h-8" onClick={() => setPasscode(newPasscode())}>
              New
            </Button>
          </div>
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={busy}>
              Save
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setEditing(false)}>
              Cancel
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
