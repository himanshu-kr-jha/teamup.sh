import { cn } from '@/shared/utils/cn';
import type { PresenceStatus } from './usePresence';

// Four states, because a dot has to be readable without being decoded.
// Somebody not connected reads as away, and somebody in a call reads as busy:
// to a colleague what matters is whether they can be reached, not the
// mechanism. The server never sends anything else; the two legacy values are
// mapped so a cached response cannot render a colourless dot.
const colours: Record<string, string> = {
  available: 'bg-green-500',
  busy: 'bg-red-500',
  away: 'bg-muted-foreground/50',
  on_leave: 'bg-sky-500',
  in_call: 'bg-red-500',
  offline: 'bg-muted-foreground/50',
};

const labels: Record<string, string> = {
  available: 'Available',
  busy: 'Busy',
  away: 'Away',
  on_leave: 'On leave',
  in_call: 'Busy',
  offline: 'Away',
};

export function presenceLabel(status: PresenceStatus, note?: string) {
  const label = labels[status] ?? 'Away';
  return note ? `${label} — ${note}` : label;
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
        colours[status] ?? colours.away,
        className
      )}
      title={label}
      aria-label={label}
      role="img"
    />
  );
}
