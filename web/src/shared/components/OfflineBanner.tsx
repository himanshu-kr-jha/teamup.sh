import { useEffect, useState } from 'react';
import { WifiOff, Loader2 } from 'lucide-react';
import { cn } from '@/shared/utils/cn';

export function OfflineBanner() {
  const [isOnline, setIsOnline] = useState(true);
  const [pendingCount, setPendingCount] = useState(0);

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
    const interval = setInterval(async () => {
      // TODO: Implement IndexedDB outbox count
      setPendingCount(0);
    }, 5000);
    return () => clearInterval(interval);
  }, []);

  if (isOnline && pendingCount === 0) return null;

  return (
    <div className={cn(
      'fixed top-0 left-0 right-0 z-50 px-4 py-2 text-center text-sm font-medium transition-colors',
      isOnline ? 'bg-yellow-100 text-yellow-800' : 'bg-red-100 text-red-800'
    )}>
      <div className="flex items-center justify-center gap-2">
        {isOnline ? (
          <>
            <Loader2 className="w-4 h-4 animate-spin" />
            Syncing {pendingCount} pending change{pendingCount !== 1 ? 's' : ''}...
          </>
        ) : (
          <>
            <WifiOff className="w-4 h-4" />
            You're offline. Changes will sync when reconnected.
          </>
        )}
      </div>
    </div>
  );
}
