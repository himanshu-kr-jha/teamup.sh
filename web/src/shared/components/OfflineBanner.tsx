import { useEffect, useState } from 'react';
import { WifiOff, Loader2 } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { useWebSocket } from '@/features/websocket/WSProvider';

/**
 * How long a dropped socket is allowed to be quietly absent.
 *
 * A reconnect usually completes in well under a second, and flashing a red bar
 * every time one does would train people to ignore it. Past this, the silence
 * is worth mentioning.
 */
const GRACE_MS = 2500;

export function OfflineBanner() {
  const [isOnline, setIsOnline] = useState(() =>
    typeof navigator === 'undefined' ? true : navigator.onLine
  );
  const [pendingCount, setPendingCount] = useState(0);
  const { state: wsState } = useWebSocket();
  // Being between sockets is normal; being between sockets for a while is not.
  const [dropAcknowledged, setDropAcknowledged] = useState(false);

  useEffect(() => {
    const handleOnline = () => setIsOnline(true);
    const handleOffline = () => setIsOnline(false);
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    return () => {
      window.removeEventListener('online', handleOnline);
      window.removeEventListener('offline', handleOffline);
    };
  }, []);

  useEffect(() => {
    if (wsState !== 'reconnecting') {
      setDropAcknowledged(false);
      return;
    }
    const timer = window.setTimeout(() => setDropAcknowledged(true), GRACE_MS);
    return () => window.clearTimeout(timer);
  }, [wsState]);

  useEffect(() => {
    const interval = setInterval(async () => {
      // TODO: Implement IndexedDB outbox count
      setPendingCount(0);
    }, 5000);
    return () => clearInterval(interval);
  }, []);

  // navigator.onLine only knows whether this machine has a network. It stays
  // true throughout a backend deploy, which is exactly when the socket is
  // down and the app looks — wrongly — like a quiet afternoon. The socket's
  // own state is the thing worth reporting.
  const disconnected = isOnline && dropAcknowledged;

  if (isOnline && !disconnected && pendingCount === 0) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        'fixed top-0 left-0 right-0 z-50 px-4 py-2 text-center text-sm font-medium transition-colors',
        isOnline ? 'bg-yellow-100 text-yellow-800' : 'bg-red-100 text-red-800'
      )}
    >
      <div className="flex items-center justify-center gap-2">
        {!isOnline ? (
          <>
            <WifiOff className="w-4 h-4" />
            You're offline. Changes will sync when reconnected.
          </>
        ) : disconnected ? (
          <>
            <Loader2 className="w-4 h-4 animate-spin" />
            Reconnecting… new messages and calls will not arrive until this clears.
          </>
        ) : (
          <>
            <Loader2 className="w-4 h-4 animate-spin" />
            Syncing {pendingCount} pending change{pendingCount !== 1 ? 's' : ''}...
          </>
        )}
      </div>
    </div>
  );
}
