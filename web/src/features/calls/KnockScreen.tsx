import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { CircleOff, DoorClosed, Loader2, LogIn, ShieldX } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { KNOCK_COOLDOWN_SECONDS, retryAfterSeconds, secondsLeft } from './knock';

/** How often to ask again while waiting, in case the answer's frame never arrived. */
const RECHECK_EVERY_MS = 15_000;

type Stage =
  | { name: 'ask' }
  | { name: 'asking' }
  | { name: 'waiting' }
  | { name: 'denied'; until: number }
  | { name: 'ended' }
  | { name: 'error'; message: string };

/**
 * The door of a private call. You ask, you wait, and when somebody lets you in you
 * go straight in, with no reload.
 *
 * The answer normally arrives as a frame. In case the socket was down at that
 * moment, asking again is safe, because a repeat returns the same request, and its
 * answer says what happened: 409 means you may join now or the call is over, and
 * the token request tells which; 429 means you were turned away.
 */
export function KnockScreen({ callId, onAdmitted }: { callId: string; onAdmitted: () => void | Promise<void> }) {
  const navigate = useNavigate();
  const { on, state: socketState } = useWebSocket();
  const [stage, setStage] = useState<Stage>({ name: 'ask' });
  const [now, setNow] = useState(() => Date.now());
  const admittedRef = useRef(onAdmitted);
  admittedRef.current = onAdmitted;

  const knock = useCallback(
    async (quiet: boolean) => {
      if (!quiet) setStage({ name: 'asking' });
      try {
        await callsApi.knock(callId);
        setStage({ name: 'waiting' });
      } catch (err) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status === 429) {
          const at = Date.now();
          setNow(at);
          setStage({ name: 'denied', until: at + retryAfterSeconds(err) * 1000 });
        } else if (status === 409) {
          void admittedRef.current();
        } else if (quiet) {
          // A failed check while waiting changes nothing. The next one may work.
        } else {
          setStage({ name: 'error', message: apiErrorMessage(err, 'Could not ask to join. Try again.') });
        }
      }
    },
    [callId]
  );

  useEffect(() => {
    return on('call.knock.result', (msg) => {
      const payload = msg.payload as { callId?: string; status?: string };
      if (payload?.callId !== callId) return;
      if (payload.status === 'admitted') void admittedRef.current();
      else if (payload.status === 'denied') {
        const at = Date.now();
        setNow(at);
        setStage({ name: 'denied', until: at + KNOCK_COOLDOWN_SECONDS * 1000 });
      } else if (payload.status === 'ended') setStage({ name: 'ended' });
    });
  }, [on, callId]);

  const waiting = stage.name === 'waiting';
  useEffect(() => {
    if (!waiting) return;
    const timer = setInterval(() => void knock(true), RECHECK_EVERY_MS);
    return () => clearInterval(timer);
  }, [waiting, knock]);

  // Back from a dropped connection, the answer may have gone past while it was down.
  const socketReady = socketState === 'ready';
  useEffect(() => {
    if (waiting && socketReady) void knock(true);
    // Only when the socket comes back, not when waiting starts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [socketReady]);

  const deniedUntil = stage.name === 'denied' ? stage.until : null;
  useEffect(() => {
    if (deniedUntil === null) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [deniedUntil]);

  const back = (
    <Button variant="outline" onClick={() => navigate('/calls')}>
      Back to calls
    </Button>
  );

  return (
    <div className="flex h-full items-center justify-center overflow-y-auto p-6">
      <div className="w-full max-w-md text-center">
        {stage.name === 'ask' || stage.name === 'asking' ? (
          <>
            <DoorClosed className="mx-auto mb-3 h-10 w-10 text-primary" aria-hidden="true" />
            <h1 className="text-lg font-semibold">This is a private call</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              You are not on it yet. Ask to join and the people in the call will be told you are here.
            </p>
            <div className="mt-5 flex flex-wrap justify-center gap-2">
              {back}
              <Button disabled={stage.name === 'asking'} onClick={() => void knock(false)}>
                {stage.name === 'asking' ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />
                ) : (
                  <LogIn className="mr-2 h-4 w-4" aria-hidden="true" />
                )}
                Ask to join
              </Button>
            </div>
          </>
        ) : waiting ? (
          <div role="status">
            <Loader2 className="mx-auto mb-3 h-10 w-10 animate-spin text-primary" aria-hidden="true" />
            <h1 className="text-lg font-semibold">Waiting for someone to let you in</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              The people on this call know you are here. You will go in as soon as someone admits you, so
              you can leave this page open.
            </p>
            <div className="mt-5 flex justify-center">
              <Button variant="outline" onClick={() => navigate('/calls')}>
                Stop waiting
              </Button>
            </div>
          </div>
        ) : stage.name === 'denied' ? (
          <div role="alert">
            <ShieldX className="mx-auto mb-3 h-10 w-10 text-destructive" aria-hidden="true" />
            <h1 className="text-lg font-semibold">You were not let in</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {secondsLeft(stage.until, now) > 0
                ? `Somebody on the call said no. You can ask again in ${secondsLeft(stage.until, now)} seconds.`
                : 'Somebody on the call said no. You can ask again now.'}
            </p>
            <div className="mt-5 flex flex-wrap justify-center gap-2">
              {back}
              <Button disabled={secondsLeft(stage.until, now) > 0} onClick={() => void knock(false)}>
                Ask again
              </Button>
            </div>
          </div>
        ) : stage.name === 'ended' ? (
          <div role="alert">
            <CircleOff className="mx-auto mb-3 h-10 w-10 text-muted-foreground" aria-hidden="true" />
            <h1 className="text-lg font-semibold">This call has ended</h1>
            <p className="mt-1 text-sm text-muted-foreground">It finished before anybody answered.</p>
            <div className="mt-5 flex justify-center">{back}</div>
          </div>
        ) : (
          <div role="alert">
            <CircleOff className="mx-auto mb-3 h-10 w-10 text-destructive" aria-hidden="true" />
            <h1 className="text-lg font-semibold">Could not ask to join</h1>
            <p className="mt-1 text-sm text-muted-foreground">{stage.message}</p>
            <div className="mt-5 flex flex-wrap justify-center gap-2">
              {back}
              <Button onClick={() => void knock(false)}>Try again</Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
