import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, KeyRound, Link2, Loader2, Mail, RefreshCw, UserPlus, X } from 'lucide-react';
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

const full = (path?: string) => `${window.location.origin}${path ?? ''}`;

/** The invitation to paste into an email or a message: the direct link, and the passcode one if that fails. */
export function invitation(link: GuestLink, title?: string): string {
  const lines = [`Join ${title ? `"${title}"` : 'the meeting'} from your browser, no account needed:`, full(link.directPath ?? link.path)];
  if (link.passcode && link.path) {
    lines.push('', `If that link does not work, open ${full(link.path)} and enter the passcode ${link.passcode}.`);
  }
  return lines.join('\n');
}

/** Addresses typed or pasted: separated by commas, semicolons, spaces or lines. */
export function splitEmails(text: string): string[] {
  return text
    .split(/[\s,;]+/)
    .map((e) => e.trim())
    .filter(Boolean);
}

/**
 * A meeting's guest links: people from outside the organization join with
 * them under their own name. The direct link needs nothing more; the other asks
 * for the passcode, for when the direct one cannot be used. The organizer and
 * administrators turn them on and off and invite people by email; everybody in
 * the meeting can pass them on.
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
  const [emails, setEmails] = useState('');

  const apply = async (work: () => Promise<{ data: GuestLink }>, done: string) => {
    setBusy(true);
    try {
      const res = await work();
      queryClient.setQueryData(['guest-link', callId], res.data);
      setEditing(false);
      toast.success(done);
      return true;
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not change the guest link.'));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const change = (data: { enabled: boolean; passcode?: string; newLink?: boolean }, done: string) =>
    apply(() => callsApi.setGuestLink(callId, data), done);

  const copy = async (text: string, what: string) => {
    if (await copyText(text)) toast.success(`${what} copied.`);
    else toast.error('Could not copy. Your browser did not allow it.');
  };

  if (isPending) return <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-label="Loading" />;
  if (error || !link) return null;

  const invite = async () => {
    const list = splitEmails(emails);
    if (list.length === 0) return;
    const ok = await apply(
      () => callsApi.inviteGuests(callId, list),
      link.mailConfigured ? `Invitation emailed to ${list.length === 1 ? list[0] : `${list.length} people`}.` : 'Added. Email is not set up, so send them the invitation yourself.'
    );
    if (ok) setEmails('');
  };

  const inviteForm = link.canManage && (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        void invite();
      }}
    >
      <Label htmlFor={`invite-${callId}`}>Invite by email</Label>
      <div className="flex gap-2">
        <Input
          id={`invite-${callId}`}
          type="text"
          inputMode="email"
          value={emails}
          onChange={(e) => setEmails(e.target.value)}
          placeholder="name@company.com, another@company.com"
          autoComplete="off"
          className="h-8 min-w-0 flex-1"
        />
        <Button type="submit" size="sm" className="h-8 shrink-0" disabled={busy || splitEmails(emails).length === 0}>
          <Mail className="mr-2 h-4 w-4" />
          {link.mailConfigured ? 'Send' : 'Add'}
        </Button>
      </div>
      {!link.mailConfigured && (
        <p className="text-xs text-muted-foreground">Email is not set up for this organization, so copy the invitation and send it yourself.</p>
      )}
    </form>
  );

  const invited = link.invites.length > 0 && (
    <ul className="space-y-1" aria-label="Invited by email">
      {link.invites.map((i) => (
        <li key={i.email} className="flex min-w-0 items-center gap-2">
          <Mail className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="min-w-0 flex-1 truncate">{i.email}</span>
          <span className="shrink-0 text-xs text-muted-foreground">{i.emailedAt ? 'emailed' : 'not emailed'}</span>
          {link.canManage && (
            <button
              type="button"
              className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
              aria-label={`Take ${i.email} off the invitations`}
              disabled={busy}
              onClick={() => apply(() => callsApi.removeGuestInvite(callId, i.email), `${i.email} taken off.`)}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </li>
      ))}
    </ul>
  );

  if (!link.enabled) {
    return (
      <section aria-label="Guests" className="space-y-3 rounded-lg border border-border p-3 text-sm">
        <p className="flex items-center gap-2 font-medium">
          <UserPlus className="h-4 w-4 shrink-0" aria-hidden="true" />
          Guests
        </p>
        {link.canManage ? (
          <>
            <p className="text-muted-foreground">
              Let people from outside the organization join with a link. They give their name, and come in once the meeting has started.
            </p>
            <Button size="sm" disabled={busy} onClick={() => change({ enabled: true, passcode: newPasscode() }, 'Guest links made.')}>
              {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Link2 className="mr-2 h-4 w-4" />}
              Make guest links
            </Button>
            {inviteForm}
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
        Guests can join with these links
      </p>
      <LinkRow label="Direct link" hint="No passcode needed" url={full(link.directPath)} onCopy={() => copy(full(link.directPath), 'Direct link')} />
      <LinkRow
        label="Link with passcode"
        hint={link.passcode ? 'If the direct link does not work' : 'No passcode is set: anybody with it can join'}
        url={full(link.path)}
        onCopy={() => copy(full(link.path), 'Link')}
      />
      {link.passcode && (
        <p className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="text-muted-foreground">Passcode</span>
          <span className="font-mono font-medium tracking-wider" data-testid="guest-passcode">
            {link.passcode}
          </span>
        </p>
      )}
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
              onClick={() => change({ enabled: true, newLink: true }, 'New links made. The old ones no longer work.')}
              title="The old links stop working; guests already in stay"
            >
              <RefreshCw className="mr-2 h-4 w-4" />
              New links
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="text-destructive hover:text-destructive"
              disabled={busy}
              onClick={() => change({ enabled: false }, 'Guest links turned off. Any guests have been taken out.')}
            >
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
      {inviteForm}
      {invited}
    </section>
  );
}

function LinkRow({ label, hint, url, onCopy }: { label: string; hint: string; url: string; onCopy: () => void }) {
  return (
    <div className="space-y-1">
      <p className="text-xs">
        <span className="font-medium">{label}</span>
        <span className="text-muted-foreground"> · {hint}</span>
      </p>
      <div className="flex min-w-0 items-center gap-2">
        <Input readOnly value={url} aria-label={label} className="h-8 min-w-0 flex-1 font-mono text-xs" onFocus={(e) => e.target.select()} />
        <Button size="sm" variant="outline" className="h-8 shrink-0" onClick={onCopy} aria-label={`Copy the ${label.toLowerCase()}`}>
          <Copy className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}
