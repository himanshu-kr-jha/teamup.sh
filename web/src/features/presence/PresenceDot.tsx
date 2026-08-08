import { cn } from '@/shared/utils/cn';
import type { PresenceStatus } from './usePresence';

const colours: Record<PresenceStatus, string> = {
  online: 'bg-green-500',
  away: 'bg-yellow-500',
  busy: 'bg-red-500',
  offline: 'bg-muted-foreground/40',
};

const labels: Record<PresenceStatus, string> = {
  online: 'Online',
  away: 'Away',
  busy: 'Busy',
  offline: 'Offline',
};

/** A small status dot, sized to sit on the corner of an avatar. */
export function PresenceDot({
  status,
  className,
}: {
  status: PresenceStatus;
  className?: string;
}) {
  return (
    <span
      className={cn('inline-block h-2.5 w-2.5 rounded-full ring-2 ring-background', colours[status], className)}
      title={labels[status]}
      aria-label={labels[status]}
      role="img"
    />
  );
}
