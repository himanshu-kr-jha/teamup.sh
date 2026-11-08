import { useEffect, useState } from 'react';
import { useIsFetching, useIsMutating } from '@tanstack/react-query';
import { useNavigation } from 'react-router-dom';
import { cn } from '@/shared/utils/cn';

/** How long a wait goes unremarked: a quick answer should not flash a bar. */
const GRACE_MS = 350;

/**
 * A thin bar in the organization's colour along the top of the window,
 * whenever the page is waiting on something: moving to another page, a page's
 * first data, or saving. Not for the checks some pages repeat in the
 * background, which would flash it every few seconds.
 */
export function TopProgress() {
  const navigating = useNavigation().state !== 'idle';
  const loading = useIsFetching({ predicate: (q) => q.state.data === undefined && q.state.fetchStatus === 'fetching' }) > 0;
  const saving = useIsMutating() > 0;
  const busy = navigating || loading || saving;
  const [shown, setShown] = useState(false);

  useEffect(() => {
    if (!busy) {
      setShown(false);
      return;
    }
    const t = window.setTimeout(() => setShown(true), GRACE_MS);
    return () => window.clearTimeout(t);
  }, [busy]);

  return (
    <div
      className={cn(
        'pointer-events-none fixed inset-x-0 top-0 z-[100] h-0.5 overflow-hidden transition-opacity duration-300',
        shown ? 'opacity-100' : 'opacity-0'
      )}
      role="progressbar"
      aria-hidden={!shown}
      aria-label="Loading"
      data-testid="top-progress"
      data-busy={shown || undefined}
    >
      {/* Only while it shows: an endless animation on an invisible bar would
          still cost a phone's battery, and nothing waiting for the page's
          animations to end would ever see it settle. */}
      {shown && (
        <span className="absolute inset-y-0 left-0 w-1/3 animate-loading-bar motion-reduce:w-full motion-reduce:animate-pulse bg-primary shadow-[0_0_8px_hsl(var(--primary))]" />
      )}
    </div>
  );
}
