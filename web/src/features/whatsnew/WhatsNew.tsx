import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { ArrowRight, CalendarDays, CheckSquare, GitPullRequest, MessageSquare, Sparkles, Video, type LucideIcon } from 'lucide-react';
import { format, parseISO } from 'date-fns';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { whatsNewApi } from '@/shared/api/client';
import { useAuthStore } from '@/shared/store/authStore';
import { useAuth } from '@/features/auth/AuthProvider';
import { useActiveCall } from '@/features/calls/ActiveCallProvider';
import { cn } from '@/shared/utils/cn';
import { LATEST, RELEASES, unseen, type Area, type Change, type Release } from './releases';

const AREAS: Record<Area, { Icon: LucideIcon; label: string; tone: string }> = {
  meetings: { Icon: Video, label: 'Meetings', tone: 'bg-violet-500/10 text-violet-600 dark:text-violet-400' },
  chat: { Icon: MessageSquare, label: 'Chat', tone: 'bg-sky-500/10 text-sky-600 dark:text-sky-400' },
  tasks: { Icon: CheckSquare, label: 'Tasks', tone: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' },
  calendar: { Icon: CalendarDays, label: 'Calendar', tone: 'bg-amber-500/10 text-amber-600 dark:text-amber-400' },
  code: { Icon: GitPullRequest, label: 'Code', tone: 'bg-rose-500/10 text-rose-600 dark:text-rose-400' },
  app: { Icon: Sparkles, label: 'TeamUp', tone: 'bg-primary/10 text-primary' },
};

function dayOf(release: Release) {
  return format(parseISO(release.date), 'd MMMM yyyy');
}

/** Records, for this person on every device, that they have seen the newest entry. */
function useMarkSeen() {
  const setUser = useAuthStore((s) => s.setUser);
  return useCallback(async () => {
    const user = useAuthStore.getState().user;
    if (!user || (user.whatsNewSeen && user.whatsNewSeen >= LATEST)) return;
    // Marked here at once, so it does not open again while the server is asked.
    setUser({ ...user, whatsNewSeen: LATEST });
    try {
      await whatsNewApi.seen(LATEST);
    } catch {
      // Shown again next time rather than lost; nothing else depends on it.
    }
  }, [setUser]);
}

function ChangeItem({ change, onGo }: { change: Change; onGo?: () => void }) {
  const area = AREAS[change.area];
  return (
    <li className="flex gap-3" data-testid="whats-new-change">
      <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-lg', area.tone)} title={area.label}>
        <area.Icon className="h-4 w-4" aria-hidden="true" />
      </span>
      <div className="min-w-0 flex-1">
        <h3 className="text-sm font-semibold">{change.title}</h3>
        <p className="mt-0.5 text-sm leading-relaxed text-muted-foreground">{change.body}</p>
        {change.link && (
          <Link to={change.link} onClick={onGo} className="mt-1 inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline">
            Try it
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </Link>
        )}
      </div>
    </li>
  );
}

/**
 * Opens by itself once after each update with what changed since the person
 * last looked: not for somebody still to take the welcome tour, who has not
 * seen the app before, nor in the middle of a call. Closing it, or opening the
 * full page, counts as having seen it.
 */
export function WhatsNewAfterUpdate() {
  const user = useAuthStore((s) => s.user);
  const { isLoading } = useAuth();
  const { activeCall } = useActiveCall();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const markSeen = useMarkSeen();
  const [open, setOpen] = useState(false);

  // Undefined is not yet known, as for a page holding the user from before
  // this existed: nothing opens until the server has said.
  const known = !isLoading && !!user && user.whatsNewSeen !== undefined && !!user.tourCompletedAt;
  const fresh = known ? unseen(user.whatsNewSeen) : [];
  const due = fresh.length > 0 && !activeCall && !pathname.startsWith('/calls/') && pathname !== '/whats-new';

  useEffect(() => {
    if (!due) return;
    const t = setTimeout(() => setOpen(true), 800);
    return () => clearTimeout(t);
  }, [due]);

  const close = () => {
    setOpen(false);
    void markSeen();
  };

  if (!open || fresh.length === 0) return null;
  const changes = fresh.flatMap((r) => r.changes);
  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent className="flex max-h-[85dvh] max-w-lg flex-col gap-0 p-0" data-testid="whats-new-dialog">
        <DialogHeader className="shrink-0 border-b border-border p-5 pr-12 text-left">
          <p className="flex items-center gap-1.5 text-xs font-medium text-primary">
            <Sparkles className="h-3.5 w-3.5" aria-hidden="true" />
            What’s new
          </p>
          <DialogTitle className="text-lg">{fresh[0].title}</DialogTitle>
          <DialogDescription>
            {fresh.length === 1 ? `Updated ${dayOf(fresh[0])}.` : `${fresh.length} updates since you last looked.`}
          </DialogDescription>
        </DialogHeader>
        <ul className="min-h-0 flex-1 space-y-4 overflow-y-auto p-5">
          {changes.map((c, i) => (
            <ChangeItem key={i} change={c} onGo={close} />
          ))}
        </ul>
        <div className="flex shrink-0 flex-col-reverse gap-2 border-t border-border p-4 sm:flex-row sm:justify-end">
          <Button
            variant="outline"
            onClick={() => {
              close();
              navigate('/whats-new');
            }}
          >
            All updates
          </Button>
          <Button onClick={close}>Got it</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** Every update, newest first, for whoever wants to look back. */
export function WhatsNewPage() {
  const markSeen = useMarkSeen();
  useEffect(() => {
    void markSeen();
  }, [markSeen]);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl space-y-10 px-4 py-6 md:px-6 md:py-10">
        <header>
          <p className="flex items-center gap-1.5 text-sm font-medium text-primary">
            <Sparkles className="h-4 w-4" aria-hidden="true" />
            Updates
          </p>
          <h1 className="mt-1 text-2xl font-bold">What’s new in TeamUp</h1>
          <p className="mt-1 text-sm text-muted-foreground">What changed, newest first.</p>
        </header>
        {RELEASES.map((r) => (
          <section key={r.id} aria-labelledby={`release-${r.id}`} className="space-y-4" data-testid="whats-new-release">
            <div className="border-b border-border pb-2">
              <time dateTime={r.date} className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                {dayOf(r)}
              </time>
              <h2 id={`release-${r.id}`} className="text-lg font-semibold">
                {r.title}
              </h2>
            </div>
            <ul className="space-y-4">
              {r.changes.map((c, i) => (
                <ChangeItem key={i} change={c} />
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  );
}
