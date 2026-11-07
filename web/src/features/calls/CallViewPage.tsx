import { useEffect, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, CalendarClock, CircleOff, Loader2, Video } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { useAuth } from '@/features/auth/AuthProvider';
import { JOIN_EARLY_MINUTES } from '@/features/calendar/layout';
import { useActiveCall } from './ActiveCallProvider';
import { KnockScreen } from './KnockScreen';
import { lobbyFor } from './lobby';

/** How often a lobby looks again, to say that somebody has started it. */
const LOBBY_RECHECK_MS = 30_000;

/**
 * The call's own page. The room itself lives in ActiveCallProvider, above the
 * router, so that navigating away shrinks the call into a corner instead of
 * dropping it. All this page does is ask to be in the call and leave room for
 * it on screen, or show the door when that was refused, or stop at a lobby when
 * walking in would start a scheduled meeting nobody meant to start.
 */
export function CallViewPage() {
  const { roomName: callId } = useParams<{ roomName: string }>();
  return callId ? <CallRoute key={callId} callId={callId} /> : null;
}

/** Whoever pressed a Join button has already decided. A link has decided nothing. */
function pressedJoin(state: unknown): boolean {
  return (state as { join?: boolean } | null)?.join === true;
}

function CallRoute({ callId }: { callId: string }) {
  const navigate = useNavigate();
  const location = useLocation();
  const { user } = useAuth();
  const { activeCall, join, joining, failure, clearFailure } = useActiveCall();
  const inCall = activeCall?.callId === callId;
  // null until it is known whether to walk straight in.
  const [choice, setChoice] = useState<'join' | 'lobby' | null>(() => (pressedJoin(location.state) || inCall ? 'join' : null));

  const { data: call, error, isFetchedAfterMount, refetch } = useQuery({
    queryKey: ['call', callId],
    queryFn: () => callsApi.get(callId).then((res) => res.data),
    enabled: choice !== 'join',
    // A copy cached before somebody else opened it, or before you were let in, cannot
    // decide this.
    staleTime: 0,
    retry: false,
    refetchInterval: choice === 'lobby' ? LOBBY_RECHECK_MS : false,
  });

  const lobby = call ? lobbyFor(call, user?.id, new Date()) : 'none';

  useEffect(() => {
    if (choice !== null) return;
    if (error) {
      // Joining is what says why it cannot be read, in the words for that.
      setChoice('join');
    } else if (isFetchedAfterMount && call) {
      setChoice(lobby === 'none' ? 'join' : 'lobby');
    }
  }, [choice, error, isFetchedAfterMount, call, lobby]);

  useEffect(() => {
    if (choice === 'join') void join(callId);
  }, [choice, callId, join]);

  if (choice === 'lobby') {
    return <MeetingLobby callId={callId} call={call} early={lobby === 'early'} onJoin={() => setChoice('join')} />;
  }

  if (choice === 'join' && failure?.kind === 'knock') {
    return (
      <KnockScreen
        callId={callId}
        onAdmitted={async () => {
          // They asked to come in, so in they go, and asking is deciding. But not into
          // a meeting days away, which their arriving would lock against moving: for
          // that, being let in means being invited.
          clearFailure();
          const { data: fresh } = await refetch();
          if (fresh && lobbyFor(fresh, user?.id, new Date()) === 'early') setChoice('lobby');
          else void join(callId);
        }}
      />
    );
  }

  if (choice === 'join' && failure) {
    const over = failure.kind === 'over';
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-md text-center">
          {over ? (
            <CircleOff className="mx-auto mb-3 h-8 w-8 text-muted-foreground" aria-hidden="true" />
          ) : (
            <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-destructive" aria-hidden="true" />
          )}
          <p className="font-medium" role="alert">
            {over
              ? /cancel/i.test(failure.message)
                ? 'This meeting was cancelled.'
                : 'This call has ended.'
              : failure.kind === 'missing'
                ? 'This call does not exist, or is not in your organization.'
                : failure.message}
          </p>
          <Button className="mt-4" onClick={() => navigate('/calls')}>
            Back to calls
          </Button>
        </div>
      </div>
    );
  }

  if (choice !== 'join' || joining || !inCall) {
    return (
      <div role="status" aria-label="Joining the call" className="flex h-full items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-hidden="true" />
      </div>
    );
  }

  // The provider draws the call over this space.
  return <div className="h-full" />;
}

/**
 * A scheduled meeting, as somebody who may join it sees it before anybody has. Joining
 * is a click, because joining starts it, and a meeting that has started can no longer
 * be moved. It is also where a knock notification leads: somebody at the door can be
 * answered from here (KnockCenter) without starting anything.
 */
function MeetingLobby({
  callId,
  call,
  early,
  onJoin,
}: {
  callId: string;
  call: { title?: string; status?: string; scheduledStart?: string } | undefined;
  early: boolean;
  onJoin: () => void;
}) {
  const navigate = useNavigate();
  const { watchDoor } = useActiveCall();

  useEffect(() => {
    watchDoor(callId);
    return () => watchDoor(null);
  }, [callId, watchDoor]);

  const started = call?.status === 'active';
  const start = call?.scheduledStart;
  return (
    <div className="flex h-full items-center justify-center overflow-y-auto p-6">
      <div className="w-full max-w-md text-center">
        <CalendarClock className="mx-auto mb-3 h-10 w-10 text-primary" aria-hidden="true" />
        <h1 className="break-anywhere text-lg font-semibold">{call?.title?.trim() || 'Scheduled meeting'}</h1>
        {start && (
          <p className="mt-1 text-sm tabular-nums text-muted-foreground">
            {formatDate(start)} at {formatTime(start)}
          </p>
        )}
        <p className="mt-3 text-sm text-muted-foreground">
          {started
            ? 'Somebody has started it.'
            : early
              ? `It opens ${JOIN_EARLY_MINUTES} minutes before it starts. Joining now starts it for everybody, and it can no longer be moved.`
              : 'Nobody has started it yet. Joining starts it for everybody.'}
        </p>
        <div className="mt-5 flex flex-wrap justify-center gap-2">
          <Button variant="outline" onClick={() => navigate('/calls')}>
            Back to calls
          </Button>
          <Button onClick={onJoin}>
            <Video className="mr-2 h-4 w-4" aria-hidden="true" />
            {early && !started ? 'Join now' : 'Join'}
          </Button>
        </div>
      </div>
    </div>
  );
}
