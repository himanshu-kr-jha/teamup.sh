import { cn } from '@/shared/utils/cn';
import type { PresenceStatus } from './usePresence';

const colours: Record<PresenceStatus, string> = {
  available: 'bg-green-500',
  in_call: 'bg-red-500',
  busy: 'bg-amber-500',
  away: 'bg-muted-foreground',
  on_leave: 'bg-sky-500',
  offline: 'bg-muted-foreground/30',
};

const labels: Record<PresenceStatus, string> = {
  available: 'Available',
  in_call: 'In a call',
  busy: 'Busy',
  away: 'Away',
  on_leave: 'On leave',
  offline: 'Offline',
};

export function presenceLabel(status: PresenceStatus, note?: string) {
  return note ? `${labels[status]} — ${note}` : labels[status];
}

/** A small status dot, sized to sit on the corner of an avatar. */
export function PresenceDot({
  status,
  note,
  className,
}: {
  status: PresenceStatus;
  /** Shown on hover, so "on leave" can say until when. */
  note?: string;
  className?: string;
}) {
  const label = presenceLabel(status, note);
  return (
    <span
      className={cn(
        'inline-block h-2.5 w-2.5 rounded-full ring-2 ring-background',
        colours[status] ?? colours.offline,
        // Offline is a hollow ring rather than a filled dot: absence should
        // not draw the eye the way a colour does.
        status === 'offline' && 'bg-transparent ring-muted-foreground/40',
        className
      )}
      title={label}
      aria-label={label}
      role="img"
    />
  );
}
