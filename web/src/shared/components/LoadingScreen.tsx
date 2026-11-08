import { useEffect, useState } from 'react';
import { BrandMark } from '@/features/branding/BrandMark';
import { cn } from '@/shared/utils/cn';

/**
 * The whole screen while TeamUp gets ready: the organization's mark and a bar
 * in its colour. index.html draws the same thing before any code has arrived,
 * so the first screen does not change when this takes over. If it goes on, it
 * says so, rather than looking stuck.
 */
export function LoadingScreen({ label = 'Loading' }: { label?: string }) {
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    const t = window.setTimeout(() => setSlow(true), 8000);
    return () => window.clearTimeout(t);
  }, []);
  return (
    <div className="flex h-viewport flex-col items-center justify-center gap-6 bg-background px-6 text-foreground" role="status" aria-live="polite" data-testid="loading-screen">
      <BrandMark size="lg" className="animate-in fade-in" />
      <LoadingBar className="w-40" />
      <p className={cn('h-5 text-sm text-muted-foreground transition-opacity duration-500', slow ? 'opacity-100' : 'opacity-0')}>
        {slow ? 'This is taking longer than usual. Check your connection if it goes on.' : ''}
      </p>
      <span className="sr-only">{label}</span>
    </div>
  );
}

/** A short bar with a stripe of the organization's colour running along it, for a wait of unknown length. */
export function LoadingBar({ className }: { className?: string }) {
  return (
    <span className={cn('relative block h-1 overflow-hidden rounded-full bg-primary/15', className)} aria-hidden="true">
      <span className="absolute inset-y-0 left-0 w-2/5 animate-loading-bar motion-reduce:w-full motion-reduce:animate-pulse rounded-full bg-primary" />
    </span>
  );
}
