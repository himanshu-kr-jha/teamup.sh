import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, CalendarClock, CalendarX, CircleOff, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { useAuth } from '@/features/auth/AuthProvider';
import { dayLabelKey } from '@/features/calendar/layout';
import { useNow } from '@/shared/hooks/useNow';
import { useActiveCall, type JoinOptions } from './ActiveCallProvider';
import { KnockScreen } from './KnockScreen';
import { lobbyFor } from './lobby';
import { PreJoinScreen } from './PreJoinScreen';
import { readJoinChoice, type JoinChoice } from './prejoin';

/** How often the screen before a call looks again at who is in it, and whether somebody started it. */
const WHO_IS_HERE_RECHECK_MS = 5_000;

/**
 * A standing meeting's token is good for fifteen minutes (memberTokenTTL); one
 * older than this is not trusted to last until the room is reached, and joining
 * asks for a new one instead.
 */
const STANDING_TOKEN_FRESH_MS = 10 * 60_000;

/**
 * What a Join button for a standing meeting hands the page: opening the day's
 * room gave a token already (useJoinStanding), kept until you press Join here.
 */
export interface StandingJoinState {
  join: true;
  standingToken: { token: string; at: number };
}

function standingTokenOf(state: unknown): StandingJoinState['standingToken'] | undefined {
  const given = (state as Partial<StandingJoinState> | null)?.standingToken;
  return given && typeof given.token === 'string' && typeof given.at === 'number' ? given : undefined;
}

/** The devices you used last, for a call that goes straight in. */
function rememberedDevices(userId: string | undefined): JoinOptions {
  const { audioInput, videoInput, audioOutput } = readJoinChoice(userId);
  return { audioInput, videoInput, audioOutput };
}

/**
 * The call's own page. The room itself lives in ActiveCallProvider, above the
 * router, so that navigating away shrinks the call into a corner instead of
 * dropping it. All this page does is decide how you come in and leave room for
 * the call on screen, or show the door when coming in was refused.
 *
 * A video call or a meeting stops first at the screen before it (PreJoinScreen),
 * where you set up your camera, microphone and background and see who is there;
 * a call goes straight in, as a phone does. The screen also stands where the
 * lobby of a scheduled meeting nobody has started used to: asking for a token is
 * what opens such a meeting, so nothing asks for one until you press Join.
 */
export function CallViewPage() {
  const { roomName: callId } = useParams<{ roomName: string }>();
  return callId ? <CallRoute key={callId} callId={callId} /> : null;
}

/** 'deciding' until the call's details say which way in; 'join' once on the way, or in. */
type Phase = 'deciding' | 'prejoin' | 'missed' | 'join';

function CallRoute({ callId }: { callId: string }) {
  const navigate = useNavigate();
  const location = useLocation();
  const { user } = useAuth();
  const { activeCall, join, enter, joining, failure, clearFailure } = useActiveCall();
  const inCall = activeCall?.callId === callId;
  // Already in it, there is nothing to set up: back to the call.
  const [phase, setPhase] = useState<Phase>(() => (inCall ? 'join' : 'deciding'));
  const standingToken = standingTokenOf(location.state);

  const { data: call, error, isFetchedAfterMount, refetch } = useQuery({
    queryKey: ['call', callId],
    queryFn: () => callsApi.get(callId).then((res) => res.data),
    enabled: phase !== 'join',
    // A copy cached before somebody else opened it, or before you were let in, cannot
    // decide this.
    staleTime: 0,
    retry: false,
    refetchInterval: phase === 'prejoin' ? WHO_IS_HERE_RECHECK_MS : false,
  });

  // Looked at again as time passes, so a screen left open turns missed at the end.
  const now = useNow();
  const lobby = call ? lobbyFor(call, user?.id, now) : 'none';

  const go = useCallback(
    (options?: JoinOptions) => {
      setPhase('join');
      void join(callId, options);
    },
    [callId, join]
  );

  useEffect(() => {
    if (phase !== 'deciding') return;
    if (error) {
      // Joining is what says why it cannot be read, in the words for that: the door
      // of a call you are not in, or that it is over.
      go(rememberedDevices(user?.id));
    } else if (isFetchedAfterMount && call) {
      if (lobby === 'missed') setPhase('missed');
      // A call is answered or placed like a phone call, with no screen in between.
      else if (call.callType === 'audio') go(rememberedDevices(user?.id));
      // Somebody who could only ask to come in goes to the door, which opens nothing.
      else if (call.canKnock === true) go();
      else setPhase('prejoin');
    }
  }, [phase, error, isFetchedAfterMount, call, lobby, go, user?.id]);

  const joinFromScreen = (choice: JoinChoice) => {
    const options: JoinOptions = {
      mic: choice.mic,
      camera: choice.camera,
      audioInput: choice.audioInput,
      videoInput: choice.videoInput,
      audioOutput: choice.audioOutput,
      standing: !!call?.standingMeetingId,
    };
    if (standingToken && Date.now() - standingToken.at < STANDING_TOKEN_FRESH_MS) {
      setPhase('join');
      enter(callId, standingToken.token, options);
    } else {
      go(options);
    }
  };

  if (phase === 'missed' || (phase === 'prejoin' && lobby === 'missed')) {
    return <MissedMeeting call={call} organizer={!!user && call?.createdBy === user.id} />;
  }

  if (phase === 'prejoin' && lobby !== 'missed') {
    return <PreJoinScreen callId={callId} call={call} lobby={lobby} onJoin={joinFromScreen} />;
  }

  if (phase === 'join' && failure?.kind === 'knock') {
    return (
      <KnockScreen
        callId={callId}
        onAdmitted={async () => {
          // They asked to come in, so in they go, and asking is deciding: with what
          // they chose last time. But not into a meeting days away, which their
          // arriving would lock against moving: for that, being let in means being
          // invited, and the screen before it says what joining would do.
          clearFailure();
          const { data: fresh } = await refetch();
          if (fresh && lobbyFor(fresh, user?.id, new Date()) === 'early') {
            setPhase('prejoin');
          } else {
            const last = readJoinChoice(user?.id);
            void join(callId, fresh?.callType === 'audio' ? rememberedDevices(user?.id) : last);
          }
        }}
      />
    );
  }

  if (phase === 'join' && failure) {
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
                : /missed/i.test(failure.message)
                  ? 'This meeting was missed. Nobody joined it before it ended.'
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

  if (phase !== 'join' || joining || !inCall) {
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
 * A scheduled meeting nobody started before its end. There is nothing to join;
 * whoever organized it can move it to a new time from the calendar.
 */
function MissedMeeting({
  call,
  organizer,
}: {
  call: { title?: string; scheduledStart?: string; scheduledEnd?: string } | undefined;
  organizer: boolean;
}) {
  const navigate = useNavigate();
  const start = call?.scheduledStart;
  const end = call?.scheduledEnd;
  return (
    <div className="flex h-full items-center justify-center overflow-y-auto p-6">
      <div className="w-full max-w-md text-center">
        <CalendarX className="mx-auto mb-3 h-10 w-10 text-muted-foreground" aria-hidden="true" />
        <h1 className="text-lg font-semibold">This meeting was missed</h1>
        {call?.title?.trim() && <p className="mt-1 break-anywhere font-medium">{call.title.trim()}</p>}
        {start && end && (
          <p className="mt-1 text-sm tabular-nums text-muted-foreground">
            {formatDate(start)}, {formatTime(start)} to {formatTime(end)}
          </p>
        )}
        <p className="mt-3 text-sm text-muted-foreground">
          Nobody joined it before it ended, so it can no longer be started.
          {organizer && ' You can move it to a new time from the calendar.'}
        </p>
        <div className="mt-5 flex flex-wrap justify-center gap-2">
          <Button variant={organizer ? 'outline' : 'default'} onClick={() => navigate('/calls')}>
            Back to calls
          </Button>
          {organizer && start && (
            <Button asChild>
              <Link to={`/calendar?view=day&date=${dayLabelKey(new Date(start))}`}>
                <CalendarClock className="mr-2 h-4 w-4" aria-hidden="true" />
                Open the calendar
              </Link>
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
