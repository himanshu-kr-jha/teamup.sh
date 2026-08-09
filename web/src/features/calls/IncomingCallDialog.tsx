import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Phone, PhoneOff, Video } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { Button } from '@/shared/components/ui/button';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useChime } from '@/shared/hooks/useChime';
import { callsApi } from '@/shared/api/client';

interface IncomingCall {
  callId: string;
  callerName: string;
  callType: string;
  channelId?: string;
}

/** How long to keep ringing before treating it as missed. */
const RING_FOR_MS = 45_000;
/** Gap between rings — a repeated ring, not one continuous tone. */
const RING_EVERY_MS = 3_000;

/**
 * Rings when somebody calls. Mounted once, above the whole app, so a call
 * reaches you wherever you are rather than only on the calls page.
 *
 * The tone is synthesised rather than loaded from a file so there is no asset
 * to ship, and it is best effort: a browser will refuse to play audio until the
 * page has been interacted with, so the dialog is the part that always works.
 */
export function IncomingCallDialog() {
  const navigate = useNavigate();
  const { on } = useWebSocket();
  // A queue rather than one slot: two people calling at once used to overwrite
  // each other, so the first call vanished without ever being answered.
  const [queue, setQueue] = useState<IncomingCall[]>([]);
  const call = queue[0] ?? null;

  const dismiss = useCallback((callId: string) => {
    setQueue((all) => all.filter((c) => c.callId !== callId));
  }, []);

  const { ring: ringOnce, stop: stopTone } = useChime();
  const ringTimerRef = useRef<ReturnType<typeof setInterval>>();
  const giveUpTimerRef = useRef<ReturnType<typeof setTimeout>>();

  const stopRinging = useCallback(() => {
    clearInterval(ringTimerRef.current);
    clearTimeout(giveUpTimerRef.current);
    stopTone();
  }, [stopTone]);

  useEffect(() => {
    return on('call.invited', (msg) => {
      const payload = msg.payload as any;
      // Your own call comes back to you so the caller lands in the room; there
      // is nothing to answer.
      if (!payload?.callId || !payload?.callerName) return;
      setQueue((all) =>
        all.some((c) => c.callId === payload.callId)
          ? all
          : [
              ...all,
              {
                callId: payload.callId,
                callerName: payload.callerName,
                callType: payload.callType ?? 'video',
                channelId: payload.channelId,
              },
            ]
      );
    });
  }, [on]);

  // The caller hung up, or the call was ended: stop ringing rather than
  // carrying on until the timeout.
  useEffect(() => {
    return on('call.ended', (msg) => {
      const payload = msg.payload as any;
      if (payload?.callId) dismiss(payload.callId);
    });
  }, [on, dismiss]);

  useEffect(() => {
    if (!call) {
      stopRinging();
      return;
    }
    ringOnce();
    ringTimerRef.current = setInterval(ringOnce, RING_EVERY_MS);
    // It rings for a while and then gives up rather than for ever.
    const missed = call.callId;
    giveUpTimerRef.current = setTimeout(() => {
      dismiss(missed);
      // Tell the caller it rang out rather than leaving them waiting on a
      // room nobody is coming to.
      callsApi.decline(missed, 'no_answer').catch(() => {
        // They will notice the empty room instead.
      });
    }, RING_FOR_MS);
    return stopRinging;
  }, [call, ringOnce, stopRinging, dismiss]);

  const answer = () => {
    const id = call!.callId;
    dismiss(id);
    navigate(`/calls/${id}`);
  };

  const decline = () => {
    const id = call!.callId;
    dismiss(id);
    // Tell whoever rang, so they are not left watching it ring out.
    callsApi.decline(id, 'declined').catch(() => {
      // They will see it time out instead.
    });
  };

  return (
    <Dialog open={!!call} onOpenChange={(open) => !open && call && decline()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {call?.callType === 'audio' ? (
              <Phone className="h-5 w-5 text-primary" aria-hidden="true" />
            ) : (
              <Video className="h-5 w-5 text-primary" aria-hidden="true" />
            )}
            Incoming {call?.callType === 'audio' ? 'call' : 'video call'}
          </DialogTitle>
          <DialogDescription>{call?.callerName} is calling.</DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2 sm:justify-center">
          <Button variant="outline" onClick={decline}>
            <PhoneOff className="mr-2 h-4 w-4" />
            Decline
          </Button>
          <Button onClick={answer}>
            <Phone className="mr-2 h-4 w-4" />
            Answer
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
