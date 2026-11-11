import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Check, Clock, KeyRound, Loader2, RefreshCw, ShieldCheck, UserMinus, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import {
  accessApi,
  orgApi,
  type AccessGrant,
  type AccessLevel,
  type AccessRequest,
  type Project,
  type RepoAccess,
} from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn, formatRelativeTime } from '@/shared/utils/cn';

const LEVELS: Array<{ value: AccessLevel; label: string; hint: string }> = [
  { value: 'read', label: 'Read', hint: 'See and clone the code' },
  { value: 'write', label: 'Write', hint: 'Push branches and changes too' },
];

const PROVIDER: Record<string, string> = { github: 'GitHub', bitbucket: 'Bitbucket' };

const REPO_STATUS: Record<RepoAccess['status'], { label: string; tone: string }> = {
  granted: { label: 'has access', tone: 'text-emerald-700 bg-emerald-50 dark:text-emerald-300 dark:bg-emerald-950/50' },
  invited: { label: 'invited on GitHub', tone: 'text-amber-800 bg-amber-50 dark:text-amber-200 dark:bg-amber-950/50' },
  failed: { label: 'not given', tone: 'text-red-700 bg-red-50 dark:text-red-300 dark:bg-red-950/50' },
  revoked: { label: 'removed', tone: 'text-muted-foreground bg-muted' },
  existing: { label: 'already has access', tone: 'text-emerald-700 bg-emerald-50 dark:text-emerald-300 dark:bg-emerald-950/50' },
  none: { label: 'no access yet', tone: 'text-muted-foreground bg-muted' },
};

/** "GitHub", "Bitbucket" or "GitHub and Bitbucket": where access found was held. */
function foundOn(grant: AccessGrant): string {
  const where = [...new Set(grant.repositories.filter((r) => r.status === 'existing').map((r) => PROVIDER[r.provider]))];
  return where.length ? where.join(' and ') : 'GitHub or Bitbucket';
}

/** "" when every repository is covered, or " on 1 of its 2 repositories". */
function foundShare(grant: AccessGrant): string {
  const have = grant.repositories.filter((r) => r.status === 'existing').length;
  return have < grant.repositories.length ? ` on ${have} of its ${grant.repositories.length} repositories` : '';
}

function invalidateAccess(queryClient: ReturnType<typeof useQueryClient>) {
  return Promise.all(
    [['project-access'], ['access-requests'], ['access-log']].map((queryKey) => queryClient.invalidateQueries({ queryKey }))
  );
}

/**
 * Your access to a project: what you hold, what you asked for, and asking for
 * more. Access to a project is access to each of its repositories, given to
 * the GitHub or Bitbucket account you linked under Settings.
 */
export function MyProjectAccess({ project, userId }: { project: Project; userId?: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [asking, setAsking] = useState(false);
  const [level, setLevel] = useState<AccessLevel>('read');
  const [reason, setReason] = useState('');
  const { data } = useQuery({ queryKey: ['project-access', project.id], queryFn: () => accessApi.project(project.id) });
  if (!data || project.repositories.length === 0) return null;

  const held = data.grants.find((g) => g.userId === userId);
  const pending = data.requests.find((r) => r.userId === userId && r.status === 'pending');

  const cancel = async () => {
    if (!pending) return;
    try {
      await accessApi.cancel(pending.id);
      await invalidateAccess(queryClient);
      toast.success('Request withdrawn.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not withdraw the request.'));
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3 text-sm">
      <KeyRound className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="mr-auto" data-testid="my-access">
        {held ? (
          <>
            {held.source === 'found' ? (
              <>
                You already have <span className="font-medium">{held.level}</span> access on {foundOn(held)}
                {foundShare(held)}
              </>
            ) : (
              <>
                You have <span className="font-medium">{held.level}</span> access
                {held.repositories.some((r) => r.status === 'invited') && ' (accept GitHub’s invitation to use it)'}
              </>
            )}
          </>
        ) : pending ? (
          <>
            You asked for <span className="font-medium">{pending.level}</span> access {formatRelativeTime(pending.createdAt)}; waiting for an administrator
          </>
        ) : (
          'You have no access to its code'
        )}
      </span>
      {pending ? (
        <Button size="sm" variant="outline" onClick={cancel}>
          Withdraw request
        </Button>
      ) : (
        (!held || held.level === 'read') && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setLevel(held ? 'write' : 'read');
              setAsking(true);
            }}
          >
            {held ? 'Ask for write access' : 'Request access'}
          </Button>
        )
      )}

      <FormDialog
        open={asking}
        onOpenChange={(o) => {
          setAsking(o);
          if (!o) setReason('');
        }}
        title={`Access to ${project.name}`}
        description={`An administrator decides. Approved, you are given ${project.repositories.length === 1 ? 'its repository' : `its ${project.repositories.length} repositories`}.`}
        submitLabel="Send request"
        onSubmit={async () => {
          await accessApi.request(project.id, { level, reason: reason.trim() });
          await invalidateAccess(queryClient);
          toast.success('Request sent. You will be told when it is answered.');
        }}
      >
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">What do you need?</legend>
          <div className="grid grid-cols-2 gap-2">
            {LEVELS.map((l) => (
              <button
                key={l.value}
                type="button"
                aria-pressed={level === l.value}
                onClick={() => setLevel(l.value)}
                className={cn(
                  'flex flex-col justify-start rounded-md border p-2 text-left text-sm transition-colors',
                  level === l.value ? 'border-primary bg-primary/10' : 'border-input hover:bg-accent'
                )}
              >
                <span className="font-medium">{l.label}</span>
                <span className="text-xs text-muted-foreground">{l.hint}</span>
              </button>
            ))}
          </div>
        </fieldset>
        <div className="space-y-2">
          <Label htmlFor="accessReason">Why (optional)</Label>
          <Textarea id="accessReason" rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Working on the sign-in fix" />
        </div>
        {data.unlinked.length > 0 && (
          <p role="alert" className="rounded-md border border-amber-500/40 bg-amber-50 p-2 text-sm text-amber-950 dark:bg-amber-950/40 dark:text-amber-100">
            Link your {data.unlinked.map((p) => PROVIDER[p]).join(' and ')} account in{' '}
            <Link to="/settings" className="font-medium underline">
              Settings
            </Link>{' '}
            first, or access there cannot be given to you.
          </p>
        )}
      </FormDialog>
    </div>
  );
}

/** An administrator's view of who can reach a project's code, and who is asking. */
export function ProjectAccessDialog({ project, onClose }: { project: Project | null; onClose: () => void }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['project-access', project?.id],
    queryFn: () => accessApi.project(project!.id),
    enabled: !!project,
  });
  const { data: usersData } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: !!project });
  const { data: logData } = useQuery({ queryKey: ['access-log', project?.id], queryFn: () => accessApi.log(project!.id), enabled: !!project });
  const [giving, setGiving] = useState(false);
  const [person, setPerson] = useState('');
  const [level, setLevel] = useState<AccessLevel>('read');
  const [busy, setBusy] = useState<string | null>(null);

  const people = (usersData?.data ?? []) as Array<{ id: string; name: string }>;

  const act = async (id: string, work: () => Promise<unknown>, done: string) => {
    setBusy(id);
    try {
      await work();
      await invalidateAccess(queryClient);
      toast.success(done);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'That did not work.'));
      await invalidateAccess(queryClient);
    } finally {
      setBusy(null);
    }
  };

  return (
    <Dialog open={!!project} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Access to {project?.name}</DialogTitle>
          <DialogDescription>
            Access is given on each of the project&apos;s repositories, to the account each person linked on GitHub or Bitbucket.
          </DialogDescription>
        </DialogHeader>
        {isLoading || !data ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <div className="space-y-5 text-sm">
            {data.requests.length > 0 && (
              <section className="space-y-2">
                <h3 className="font-semibold">Waiting for you</h3>
                <ul className="space-y-2">
                  {data.requests.map((r) => (
                    <RequestRow key={r.id} request={r} busy={busy === r.id} onApprove={() => act(r.id, () => accessApi.approve(r.id), `${r.userName} has ${r.level} access.`)}
                      onDecline={() => act(r.id, () => accessApi.decline(r.id), 'Declined; they have been told.')} />
                  ))}
                </ul>
              </section>
            )}

            <section className="space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="mr-auto font-semibold">Who has access</h3>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy !== null}
                  title="Ask GitHub and Bitbucket who can open these repositories already"
                  onClick={() =>
                    act('check', async () => {
                      const fresh = await accessApi.check(project!.id);
                      queryClient.setQueryData(['project-access', project!.id], fresh);
                    }, 'Access on GitHub and Bitbucket checked.')
                  }
                >
                  {busy === 'check' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" /> : <RefreshCw className="mr-1 h-4 w-4" aria-hidden="true" />}
                  Check now
                </Button>
                <Button size="sm" variant="outline" onClick={() => setGiving((g) => !g)}>
                  Give access
                </Button>
              </div>
              {giving && (
                <div className="flex flex-wrap items-end gap-2 rounded-md border border-border p-2">
                  <div className="min-w-[10rem] flex-1 space-y-1">
                    <Label htmlFor="givePerson">Person</Label>
                    <Select value={person} onValueChange={setPerson}>
                      <SelectTrigger id="givePerson"><SelectValue placeholder="Choose somebody" /></SelectTrigger>
                      <SelectContent>
                        {people.map((p) => (
                          <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="w-28 space-y-1">
                    <Label htmlFor="giveLevel">Access</Label>
                    <Select value={level} onValueChange={(v) => setLevel(v as AccessLevel)}>
                      <SelectTrigger id="giveLevel"><SelectValue /></SelectTrigger>
                      <SelectContent>
                        <SelectItem value="read">Read</SelectItem>
                        <SelectItem value="write">Write</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <Button
                    size="sm"
                    disabled={!person || busy !== null}
                    onClick={() =>
                      act('give', () => accessApi.grant(project!.id, { userId: person, level }), 'Access given.').then(() => {
                        setGiving(false);
                        setPerson('');
                      })
                    }
                  >
                    {busy === 'give' && <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" />}
                    Give
                  </Button>
                </div>
              )}
              {data.grants.length === 0 ? (
                <p className="text-muted-foreground">Nobody yet.</p>
              ) : (
                <ul className="space-y-2" aria-label="People with access">
                  {data.grants.map((g) => (
                    <GrantRow key={g.id} grant={g} busy={busy === g.id} onRevoke={() => act(g.id, () => accessApi.revoke(g.id), `${g.userName}'s access is removed.`)} />
                  ))}
                </ul>
              )}
            </section>

            <section className="space-y-2">
              <h3 className="font-semibold">History</h3>
              <AccessLogList entries={logData?.data ?? []} showProject={false} />
            </section>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function RequestRow({ request, busy, onApprove, onDecline }: { request: AccessRequest; busy: boolean; onApprove: () => void; onDecline: () => void }) {
  return (
    <li className="flex flex-wrap items-center gap-2 rounded-md border border-border p-2">
      <PersonAvatar userId={request.userId} name={request.userName} className="h-7 w-7" fallbackClassName="text-[10px]" decorative />
      <div className="min-w-0 flex-1">
        <p>
          <span className="font-medium">{request.userName}</span> asks for <span className="font-medium">{request.level}</span> access
          {request.projectName && <> to {request.projectName}</>}
        </p>
        <p className="break-anywhere text-xs text-muted-foreground">
          {request.reason ? `“${request.reason}” · ` : ''}
          {formatRelativeTime(request.createdAt)}
        </p>
      </div>
      <Button size="sm" variant="outline" disabled={busy} onClick={onDecline} aria-label={`Decline ${request.userName}`}>
        <X className="mr-1 h-4 w-4" aria-hidden="true" />
        Decline
      </Button>
      <Button size="sm" disabled={busy} onClick={onApprove} aria-label={`Approve ${request.userName}`}>
        {busy ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" /> : <Check className="mr-1 h-4 w-4" aria-hidden="true" />}
        Approve
      </Button>
    </li>
  );
}

function GrantRow({ grant, busy, onRevoke }: { grant: AccessGrant; busy: boolean; onRevoke: () => void }) {
  const [confirming, setConfirming] = useState(false);
  return (
    <li className="space-y-2 rounded-md border border-border p-2">
      <div className="flex flex-wrap items-center gap-2">
        <PersonAvatar userId={grant.userId} name={grant.userName} className="h-7 w-7" fallbackClassName="text-[10px]" decorative />
        <div className="min-w-0 flex-1">
          <p>
            <span className="font-medium">{grant.userName}</span> · {grant.level}
          </p>
          <p className="text-xs text-muted-foreground">
            {grant.source === 'found' ? `already had it on ${foundOn(grant)}, seen ` : grant.grantedBy ? `by ${grant.grantedBy}, ` : ''}
            {formatRelativeTime(grant.grantedAt)}
          </p>
        </div>
        {confirming ? (
          <>
            <Button size="sm" variant="outline" disabled={busy} onClick={() => setConfirming(false)}>
              Keep
            </Button>
            <Button size="sm" variant="destructive" disabled={busy} onClick={onRevoke}>
              {busy && <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" />}
              Remove access
            </Button>
          </>
        ) : (
          <Button size="sm" variant="outline" onClick={() => setConfirming(true)} aria-label={`Remove ${grant.userName}'s access`}>
            <UserMinus className="mr-1 h-4 w-4" aria-hidden="true" />
            Remove
          </Button>
        )}
      </div>
      {grant.repositories.length > 0 && (
        <ul className="space-y-1" aria-label={`${grant.userName}'s repositories`}>
          {grant.repositories.map((r) => (
            <li key={r.repository} className="flex flex-wrap items-center gap-x-2 text-xs">
              <span className="font-mono">{r.repository}</span>
              <span className={cn('rounded-full px-1.5 py-0.5 font-medium', REPO_STATUS[r.status].tone)}>{REPO_STATUS[r.status].label}</span>
              {r.detail && <span className="text-muted-foreground">{r.detail}</span>}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

type LogEntry = { id: string; projectName?: string; subject: string; actor?: string; action: string; detail?: string; at: string };

/** One line of the record, said as a sentence. */
export function describeAccess(e: LogEntry): string {
  const by = e.actor && e.actor !== e.subject ? ` by ${e.actor}` : '';
  const detail = e.detail ?? '';
  switch (e.action) {
    case 'requested':
      return `${e.subject} asked for ${detail}`;
    case 'approved':
      return `${e.subject}'s request for ${detail} access was approved${by}`;
    case 'declined':
      return `${e.subject}'s request was declined${by}${detail}`;
    case 'cancelled':
      return `${e.subject} withdrew a request`;
    case 'granted':
      return `${e.subject} was given ${detail}${by}`;
    case 'revoked':
      return `${e.subject}'s access was removed ${detail}${by}`;
    case 'failed':
      return `${e.subject}: could not change ${detail}`;
    case 'found':
      return `${e.subject} already had ${detail} on GitHub or Bitbucket`;
    case 'lost':
      return `${e.subject} no longer has access on GitHub or Bitbucket`;
    case 'outside_added':
      return `${e.subject} was added to ${detail}`;
    case 'outside_removed':
      return `${e.subject} was removed from ${detail}`;
    default:
      return `${e.subject} ${e.action} ${detail}`;
  }
}

export function AccessLogList({ entries, showProject }: { entries: LogEntry[]; showProject: boolean }) {
  if (entries.length === 0) return <p className="text-sm text-muted-foreground">Nothing yet.</p>;
  return (
    <ul className="max-h-72 space-y-1.5 overflow-y-auto text-xs" aria-label="Access history">
      {entries.map((e) => (
        <li key={e.id} className="flex gap-2">
          <Clock className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <p className="min-w-0 break-anywhere">
            {describeAccess(e)}
            <span className="text-muted-foreground">
              {showProject && e.projectName ? ` · ${e.projectName}` : ''} · {formatRelativeTime(e.at)}
            </span>
          </p>
        </li>
      ))}
    </ul>
  );
}

/** Every request waiting, across projects, at the top of the Projects tab. */
export function PendingAccessRequests() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const { data } = useQuery({ queryKey: ['access-requests'], queryFn: () => accessApi.pending() });
  const requests = data?.data ?? [];
  if (requests.length === 0) return null;
  const act = async (id: string, work: () => Promise<unknown>, done: string) => {
    setBusy(id);
    try {
      await work();
      await invalidateAccess(queryClient);
      toast.success(done);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'That did not work.'));
    } finally {
      setBusy(null);
    }
  };
  return (
    <Card className="border-primary/40">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ShieldCheck className="h-4 w-4 text-primary" aria-hidden="true" />
          {requests.length === 1 ? '1 access request' : `${requests.length} access requests`}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="space-y-2" aria-label="Access requests">
          {requests.map((r) => (
            <RequestRow key={r.id} request={r} busy={busy === r.id}
              onApprove={() => act(r.id, () => accessApi.approve(r.id), `${r.userName} has ${r.level} access to ${r.projectName}.`)}
              onDecline={() => act(r.id, () => accessApi.decline(r.id), 'Declined; they have been told.')} />
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

/** The whole organization's record of access, for administrators. */
export function AccessLogCard() {
  const { data } = useQuery({ queryKey: ['access-log', 'all'], queryFn: () => accessApi.log() });
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-base">Access history</CardTitle>
      </CardHeader>
      <CardContent>
        <AccessLogList entries={data?.data ?? []} showProject />
      </CardContent>
    </Card>
  );
}
