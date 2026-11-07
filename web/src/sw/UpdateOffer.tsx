import { useState, useSyncExternalStore } from 'react';
import { X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { useActiveCall } from '@/features/calls/ActiveCallProvider';
import { applyUpdate, isUpdateReady, subscribeToUpdate } from './register';

/** A quiet offer to move to a new version. It waits while this tab is in a call, because reloading would drop the call. */
export function UpdateOffer() {
  const ready = useSyncExternalStore(subscribeToUpdate, isUpdateReady);
  const { activeCall, joining } = useActiveCall();
  const [dismissed, setDismissed] = useState(false);

  if (!ready || dismissed || activeCall || joining) return null;
  return (
    <div
      role="status"
      className="fixed bottom-[calc(var(--toast-bottom)+1rem)] left-1/2 z-50 flex w-[calc(100%-2rem)] max-w-md -translate-x-1/2 items-center gap-2 rounded-lg border bg-card py-2 pl-4 pr-2 text-sm text-card-foreground shadow-lg"
    >
      <span className="min-w-0 flex-1">A new version of TeamUp is available.</span>
      <Button size="sm" onClick={applyUpdate}>
        Reload
      </Button>
      <Button size="icon" variant="ghost" className="h-8 w-8 shrink-0" aria-label="Not now" onClick={() => setDismissed(true)}>
        <X className="h-4 w-4" />
      </Button>
    </div>
  );
}
