import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import {
  ArrowLeft,
  ArrowRight,
  Bell,
  BellRing,
  CalendarDays,
  CheckCircle2,
  CheckSquare,
  CircleDot,
  FolderGit2,
  Loader2,
  MessageSquare,
  NotebookPen,
  PartyPopper,
  Sparkles,
  Video,
  X,
} from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { useAuthStore } from '@/shared/store/authStore';
import { tourApi, whatsNewApi } from '@/shared/api/client';
import { LATEST } from '@/features/whatsnew/releases';
import { useBranding } from '@/features/branding/useBranding';
import { usePushSubscription } from '@/features/notifications/usePushSubscription';
import { cn } from '@/shared/utils/cn';

/**
 * The tour somebody is shown the first time they sign in: a quick look around,
 * one place at a time with a light on it, then turning on notifications, then a
 * welcome. It is shown once, on whichever device they first use (the server
 * keeps that they have seen it), and Settings can show it again.
 */

type Step =
  | { kind: 'intro' }
  | { kind: 'place'; target: string; title: string; body: string; Icon: typeof Bell }
  | { kind: 'notifications' }
  | { kind: 'welcome' };

const PLACES: Extract<Step, { kind: 'place' }>[] = [
  {
    kind: 'place',
    target: 'chat',
    title: 'Chat',
    body: 'Channels for your teams and direct messages for one-to-ones. Reply in threads, send files, voice and video notes, and @mention people; two ticks mean it was read. The bell in a conversation chooses what it tells you about.',
    Icon: MessageSquare,
  },
  {
    kind: 'place',
    target: 'calls',
    title: 'Calls and meetings',
    body: 'Call anybody, or start a meeting, with guests from outside if you like. In a call, chat with everyone beside the video, and the ✨ button blurs or changes your background. Somebody already in a call is not rung; you are told they are busy.',
    Icon: Video,
  },
  {
    kind: 'place',
    target: 'meeting-notes',
    title: 'Meeting notes',
    body: 'When your organization has the note-taker on, it joins once two people are in a meeting and writes up the minutes, with the actions and who owns them, from what was said and typed in the chat. They are kept here.',
    Icon: NotebookPen,
  },
  {
    kind: 'place',
    target: 'calendar',
    title: 'Calendar',
    body: 'Your meetings, the time you block for focus, and tasks on the day they are due. Schedule a meeting and see when people are free, connect your Google Calendar, and a manager can look at their team’s calendars too.',
    Icon: CalendarDays,
  },
  {
    kind: 'place',
    target: 'tasks',
    title: 'Tasks',
    body: 'Your work and your team’s on a board, with due dates, comments, labels and the people involved. You hear, with a sound, when a task of yours changes, and Tasks shows how many have news.',
    Icon: CheckSquare,
  },
  {
    kind: 'place',
    target: 'projects',
    title: 'Projects',
    body: 'Group tasks into projects and link their GitHub or Bitbucket repositories. Each task gets a key such as TEA-2: put it in a branch or pull request and the task shows the work and moves along the board by itself.',
    Icon: FolderGit2,
  },
  {
    kind: 'place',
    target: 'availability',
    title: 'Your availability',
    body: 'Let people know whether you are around, busy or away, and for how long. It shows beside your name everywhere.',
    Icon: CircleDot,
  },
];

/** The element a step points at, if it is on screen: the sidebar on a computer, the bar along the bottom on a phone. */
function findTarget(name: string): HTMLElement | null {
  const all = document.querySelectorAll<HTMLElement>(`[data-tour="${name}"]`);
  for (const el of all) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0) return el;
  }
  return null;
}

const reducedMotion = () => typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

/** Shows the tour to somebody who has not seen it. Mounted once, inside the app's layout. */
export function WelcomeTour() {
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  // Null means not seen; undefined means not known yet, as for a page still
  // holding the user from before this existed, so nothing is shown until /me says.
  const due = !!user && user.tourCompletedAt === null;
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!due) return;
    // A moment for the page to draw, so the light has something to fall on.
    const t = setTimeout(() => setOpen(true), 600);
    return () => clearTimeout(t);
  }, [due]);

  const finish = useCallback(async () => {
    setOpen(false);
    const current = useAuthStore.getState().user;
    // Marked here at once, so it does not come back while the server is asked.
    // The tour shows the app as it is now, so What's new has nothing to add.
    void whatsNewApi.seen(LATEST).catch(() => {});
    if (current) setUser({ ...current, tourCompletedAt: new Date().toISOString(), whatsNewSeen: LATEST });
    try {
      const { data } = await tourApi.set(true);
      const latest = useAuthStore.getState().user;
      if (latest) setUser({ ...latest, tourCompletedAt: data.tourCompletedAt });
    } catch {
      // Shown again next time rather than lost; nothing else depends on it.
    }
  }, [setUser]);

  if (!open || !user) return null;
  return createPortal(<Tour name={user.name} onDone={finish} />, document.body);
}

function Tour({ name, onDone }: { name: string; onDone: () => void }) {
  const branding = useBranding();
  const org = branding?.name || 'TeamUp';
  const first = name.trim().split(/\s+/)[0] || name;

  // The places that are actually on screen here: a phone has no meeting notes in its bar.
  const steps = useMemo<Step[]>(
    () => [{ kind: 'intro' }, ...PLACES.filter((p) => findTarget(p.target)), { kind: 'notifications' }, { kind: 'welcome' }],
    []
  );
  const [index, setIndex] = useState(0);
  const step = steps[index];
  const places = steps.filter((s) => s.kind === 'place').length;
  const placeNumber = steps.slice(0, index + 1).filter((s) => s.kind === 'place').length;

  const next = useCallback(() => setIndex((i) => Math.min(steps.length - 1, i + 1)), [steps.length]);
  const back = useCallback(() => setIndex((i) => Math.max(0, i - 1)), []);

  // Where the light falls: the target, a little larger, or nowhere for the steps in the middle.
  const [rect, setRect] = useState<DOMRect | null>(null);
  useLayoutEffect(() => {
    if (step.kind !== 'place') {
      setRect(null);
      return;
    }
    const measure = () => {
      const el = findTarget(step.target);
      setRect(el ? el.getBoundingClientRect() : null);
    };
    measure();
    window.addEventListener('resize', measure);
    window.addEventListener('scroll', measure, true);
    return () => {
      window.removeEventListener('resize', measure);
      window.removeEventListener('scroll', measure, true);
    };
  }, [step]);

  // The keyboard: arrows to move, Escape to leave. Focus starts in the card and stays there.
  const card = useRef<HTMLDivElement>(null);
  useEffect(() => {
    card.current?.querySelector<HTMLElement>('[data-autofocus]')?.focus();
  }, [index]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onDone();
      } else if (e.key === 'ArrowRight' && step.kind !== 'welcome') next();
      else if (e.key === 'ArrowLeft') back();
      else if (e.key === 'Tab' && card.current) {
        const items = card.current.querySelectorAll<HTMLElement>('button:not([disabled]), a[href]');
        if (items.length === 0) return;
        const firstItem = items[0];
        const lastItem = items[items.length - 1];
        if (e.shiftKey && document.activeElement === firstItem) {
          e.preventDefault();
          lastItem.focus();
        } else if (!e.shiftKey && document.activeElement === lastItem) {
          e.preventDefault();
          firstItem.focus();
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [step.kind, next, back, onDone]);

  const pad = 6;
  const hole = rect
    ? { top: rect.top - pad, left: rect.left - pad, width: rect.width + pad * 2, height: rect.height + pad * 2 }
    : { top: window.innerHeight / 2, left: window.innerWidth / 2, width: 0, height: 0 };
  const motion = !reducedMotion();

  return (
    <div
      className="fixed inset-0 z-[100]"
      role="dialog"
      aria-modal="true"
      aria-labelledby="tour-title"
      aria-describedby="tour-body"
      data-testid="welcome-tour"
    >
      {/* The page dimmed, with a light on the place being shown. The light moves
          from one place to the next rather than jumping. */}
      <div
        className={cn('pointer-events-none absolute rounded-xl', motion && 'transition-all duration-500 ease-[cubic-bezier(0.22,1,0.36,1)]')}
        style={{ ...hole, boxShadow: '0 0 0 9999px rgb(2 6 23 / 0.62)' }}
        aria-hidden="true"
      >
        {rect && <span className={cn('absolute inset-0 rounded-xl ring-2 ring-primary', motion && 'animate-tour-pulse')} />}
      </div>
      {/* Clicks on the dimmed page go nowhere, so nothing is opened by accident. */}
      <div className="absolute inset-0" aria-hidden="true" />

      <Card key={index} cardRef={card} rect={rect} motion={motion}>
        {step.kind === 'intro' && (
          <>
            <Illustration motion={motion}>
              <Sparkles className="h-7 w-7" aria-hidden="true" />
            </Illustration>
            <h2 id="tour-title" className="text-center text-xl font-semibold">
              Hello, {first}
            </h2>
            <p id="tour-body" className="mt-2 text-center text-sm text-muted-foreground">
              Here is a quick look around {org}, a minute at most. You can leave it at any time and see it again from Settings.
            </p>
            <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row sm:justify-center">
              <Button variant="ghost" onClick={onDone}>
                Skip the tour
              </Button>
              <Button onClick={next} data-autofocus>
                Show me around
                <ArrowRight className="ml-2 h-4 w-4" aria-hidden="true" />
              </Button>
            </div>
          </>
        )}

        {step.kind === 'place' && (
          <>
            <div className="flex items-start gap-3">
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
                <step.Icon className="h-5 w-5" aria-hidden="true" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-xs font-medium text-muted-foreground">
                  {placeNumber} of {places}
                </p>
                <h2 id="tour-title" className="text-base font-semibold leading-snug">
                  {step.title}
                </h2>
              </div>
              <button
                type="button"
                onClick={onDone}
                className="-mr-1 -mt-1 rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                aria-label="Leave the tour"
              >
                <X className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
            <p id="tour-body" className="mt-3 text-sm leading-relaxed text-muted-foreground">
              {step.body}
            </p>
            <div className="mt-4 flex items-center justify-between gap-2">
              <Dots count={places} current={placeNumber - 1} />
              <div className="flex gap-2">
                <Button variant="ghost" size="sm" onClick={back} aria-label="Back">
                  <ArrowLeft className="h-4 w-4" aria-hidden="true" />
                </Button>
                <Button size="sm" onClick={next} data-autofocus>
                  Next
                  <ArrowRight className="ml-1.5 h-4 w-4" aria-hidden="true" />
                </Button>
              </div>
            </div>
          </>
        )}

        {step.kind === 'notifications' && <NotificationsStep motion={motion} onBack={back} onNext={next} />}

        {step.kind === 'welcome' && (
          <>
            {motion && <Confetti />}
            <Illustration motion={motion}>
              <PartyPopper className="h-7 w-7" aria-hidden="true" />
            </Illustration>
            <h2 id="tour-title" className="text-center text-xl font-semibold">
              Welcome to {org}, {first}!
            </h2>
            <p id="tour-body" className="mt-2 text-center text-sm text-muted-foreground">
              You are all set. Say hello to your team in Chat, check today on your Calendar, or pick up a task. What’s new, under your picture, tells you about each update. We are glad you are here.
            </p>
            <div className="mt-6 flex justify-center">
              <Button onClick={onDone} data-autofocus className="min-w-[10rem]">
                Get started
              </Button>
            </div>
          </>
        )}
      </Card>
    </div>
  );
}

/**
 * The card: beside the light on a computer, above it on a phone, where the light
 * is on the bar along the bottom; in the middle for the steps that are not about
 * one place. It slides in a little each time the step changes.
 */
function Card({
  rect,
  motion,
  cardRef,
  children,
}: {
  rect: DOMRect | null;
  motion: boolean;
  cardRef: React.RefObject<HTMLDivElement>;
  children: ReactNode;
}) {
  const width = Math.min(360, window.innerWidth - 32);
  let style: React.CSSProperties;
  let from = 'slide-in-from-bottom-2';
  if (!rect) {
    style = { left: '50%', top: '50%', transform: 'translate(-50%, -50%)', width: Math.min(420, window.innerWidth - 32) };
    from = 'zoom-in-95';
  } else if (rect.right + 16 + width <= window.innerWidth - 16) {
    // Beside it.
    style = { left: rect.right + 18, top: Math.max(16, Math.min(rect.top - 12, window.innerHeight - 280)), width };
    from = 'slide-in-from-left-2';
  } else if (rect.top > window.innerHeight / 2) {
    // Above it.
    style = {
      left: Math.max(16, Math.min(rect.left + rect.width / 2 - width / 2, window.innerWidth - width - 16)),
      bottom: window.innerHeight - rect.top + 18,
      width,
    };
  } else {
    // Below it.
    style = {
      left: Math.max(16, Math.min(rect.left + rect.width / 2 - width / 2, window.innerWidth - width - 16)),
      top: rect.bottom + 18,
      width,
    };
    from = 'slide-in-from-top-2';
  }
  return (
    <div
      ref={cardRef}
      className={cn(
        'absolute overflow-hidden rounded-2xl border border-border bg-popover p-5 text-popover-foreground shadow-2xl',
        motion && `animate-in fade-in duration-300 ${from}`
      )}
      style={style}
    >
      {children}
    </div>
  );
}

function Illustration({ motion, children }: { motion: boolean; children: ReactNode }) {
  return (
    <div className="mb-4 flex justify-center" aria-hidden="true">
      <span className="relative flex h-16 w-16 items-center justify-center">
        {/* A halo that breathes within its own space, so the card's edge never cuts it. */}
        <span className={cn('absolute inset-0 rounded-full bg-primary/15', motion && 'animate-tour-pulse')} />
        <span className="relative flex h-14 w-14 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-lg">
          {children}
        </span>
      </span>
    </div>
  );
}

function Dots({ count, current }: { count: number; current: number }) {
  return (
    <span className="flex items-center gap-1.5" aria-hidden="true">
      {Array.from({ length: count }, (_, i) => (
        <span
          key={i}
          className={cn('h-1.5 rounded-full transition-all duration-300', i === current ? 'w-4 bg-primary' : 'w-1.5 bg-muted-foreground/30')}
        />
      ))}
    </span>
  );
}

/**
 * Turning notifications on, which a browser allows only from a click. One button
 * does both kinds: while TeamUp is open in a tab, and, where the browser and the
 * server allow it, while it is closed. Each way it can end is said plainly.
 */
function NotificationsStep({ motion, onBack, onNext }: { motion: boolean; onBack: () => void; onNext: () => void }) {
  const { state, busy, enable } = usePushSubscription();
  const permission = typeof Notification === 'undefined' ? 'unsupported' : Notification.permission;
  // Allowed is on: notifications reach a tab that is open. Push, for when it is
  // closed, also needs the browser and the server, and is said when it is there.
  const on = state === 'on' || (permission === 'granted' && state !== 'checking');

  let message: string;
  if (state === 'checking') message = 'One moment…';
  else if (state === 'on') message = 'Notifications are on. Calls and messages reach you on this device, even when TeamUp is closed.';
  else if (on) message = 'Notifications are on while TeamUp is open in a tab.';
  else if (state === 'denied' || permission === 'denied')
    message = 'Your browser is blocking notifications for this site. Allow them from the icon beside the address bar, then come back to this step.';
  else if (state === 'unsupported' || permission === 'unsupported')
    message = 'This browser cannot show notifications. On a phone, add TeamUp to your home screen first, then turn them on from Settings.';
  else message = 'Know when somebody calls, mentions you or sends you a message, even when you are on another tab.';

  return (
    <>
      <Illustration motion={motion && !on}>
        {on ? <CheckCircle2 className="h-7 w-7" aria-hidden="true" /> : <BellRing className={cn('h-7 w-7', motion && 'animate-tour-ring')} aria-hidden="true" />}
      </Illustration>
      <h2 id="tour-title" className="text-center text-xl font-semibold">
        {on ? 'You will be notified' : 'Turn on notifications'}
      </h2>
      <p id="tour-body" className="mt-2 text-center text-sm text-muted-foreground" role="status">
        {message}
      </p>
      <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row sm:justify-center">
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft className="mr-1.5 h-4 w-4" aria-hidden="true" />
          Back
        </Button>
        {on || state === 'denied' || state === 'unsupported' || permission === 'denied' ? (
          <Button onClick={onNext} data-autofocus>
            Continue
            <ArrowRight className="ml-2 h-4 w-4" aria-hidden="true" />
          </Button>
        ) : (
          <>
            <Button variant="outline" onClick={onNext}>
              Not now
            </Button>
            <Button onClick={() => void enable()} disabled={busy || state === 'checking'} data-autofocus>
              {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <Bell className="mr-2 h-4 w-4" aria-hidden="true" />}
              Turn on notifications
            </Button>
          </>
        )}
      </div>
    </>
  );
}

/** A small burst of the organization's colour and a few friends, for the welcome. */
function Confetti() {
  const pieces = useMemo(
    () =>
      Array.from({ length: 28 }, (_, i) => ({
        left: (i * 37) % 100,
        delay: (i % 7) * 70,
        duration: 1400 + ((i * 53) % 900),
        drift: ((i * 29) % 60) - 30,
        colour: ['hsl(var(--primary))', '#f59e0b', '#10b981', '#ec4899', '#3b82f6'][i % 5],
        size: 6 + (i % 3) * 2,
        turn: (i * 47) % 360,
      })),
    []
  );
  return (
    <span className="pointer-events-none absolute inset-x-0 top-0 h-40 overflow-hidden" aria-hidden="true">
      {pieces.map((p, i) => (
        <span
          key={i}
          className="absolute top-0 block animate-tour-confetti rounded-[2px] opacity-0"
          style={
            {
              left: `${p.left}%`,
              width: p.size,
              height: p.size * 0.45,
              backgroundColor: p.colour,
              animationDelay: `${p.delay}ms`,
              animationDuration: `${p.duration}ms`,
              '--drift': `${p.drift}px`,
              '--turn': `${p.turn}deg`,
            } as React.CSSProperties
          }
        />
      ))}
    </span>
  );
}
