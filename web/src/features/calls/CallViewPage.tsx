import { useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { AlertTriangle, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { useActiveCall } from './ActiveCallProvider';

/**
 * The call's own page. The room itself lives in ActiveCallProvider, above the
 * router, so that navigating away shrinks the call into a corner instead of
 * dropping it. All this page does is ask to be in the call and leave room for
 * it on screen.
 */
export function CallViewPage() {
  const { roomName: callId } = useParams<{ roomName: string }>();
  const navigate = useNavigate();
  const { activeCall, join, joining, error } = useActiveCall();

  useEffect(() => {
    if (callId) void join(callId);
  }, [callId, join]);

  if (error) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-md text-center">
          <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-destructive" aria-hidden="true" />
          <p className="font-medium">{error}</p>
          <Button className="mt-4" onClick={() => navigate('/calls')}>
            Back to calls
          </Button>
        </div>
      </div>
    );
  }

  if (joining || activeCall?.callId !== callId) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-hidden="true" />
      </div>
    );
  }

  // The provider draws the call over this space.
  return <div className="h-full" />;
}
