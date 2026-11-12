import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  ReactNode,
} from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  LiveKitRoom,
  RoomAudioRenderer,
  useAudioPlayback,
  useRemoteParticipants,
  useRoomContext,
} from '@livekit/components-react';
import { ConnectionState, RoomEvent } from 'livekit-client';
import { useQuery } from '@tanstack/react-query';
import { CallStage } from './CallStage';
import { MeetingChatButton, MeetingChatProvider, StageWithChat, UnreadChatMark, type ChatTransport } from './MeetingChat';
import { NotesBanner } from './notetaker';
import { CallControls, CompactCallControls, ShareUnavailableNote } from './CallControls';
import { GuestAccess } from './GuestAccess';
import { CallLogDialog } from './CallLogDialog';
import { copyCallLink } from './copyLink';
import { classifyJoinError, type JoinFailure } from './knock';
import { BURST_WINDOW_MS, createJoinTracker, joinedMessage } from './joinPing';
import {
  AlertTriangle,
  History,
  Link2,
  Maximize2,
  Minimize2,
  MoveDiagonal2,
  PhoneOff,
  Volume2,
  UserPlus,
} from 'lucide-react';
import '@livekit/components-styles';
import { Button } from '@/shared/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { callsApi, presenceApi } from '@/shared/api/client';
import { useChime } from '@/shared/hooks/useChime';
import { playSound, preloadSounds } from '@/shared/sounds';
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';
import { useToast } from '@/shared/hooks/useToast';
import { useUIStore } from '@/shared/store';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { cn } from '@/shared/utils/cn';
import { CameraBackground } from './BackgroundPicker';
import { RESIZE_STEP, fitCallWindow, keepSize, savedSize, viewport } from './callWindow';

interface ActiveCall {
  callId: string;
  token: string;
  /** A standing meeting's room has no members, so nobody there answers knocks. */
  standing?: boolean;
  /**
   * A call, as opposed to a video call or a meeting: the microphone is on from
   * the start, as on a phone, and the camera stays off until somebody turns it on.
   */
  voice?: boolean;
}

interface ActiveCallContextValue {
  activeCall: ActiveCall | null;
  /** Joins a call, or does nothing if already in it. */
  join: (callId: string) => Promise<void>;
  /** Goes into a standing meeting's room, with the token its join gave. */
  enter: (callId: string, token: string) => void;
  /** Leaves without ending it for anybody else. */
  leave: () => void;
  joining: boolean;
  /** Why the last attempt to join did not work. */
  failure: JoinFailure | null;
  /** Forgets why, for a page deciding afresh whether to join. */
  clearFailure: () => void;
  /** The call whose door somebody is watching from its page, without being in it. */
  watchedDoor: string | null;
  watchDoor: (callId: string | null) => void;
}

const ActiveCallContext = createContext<ActiveCallContextValue | undefined>(undefined);

export function useActiveCall() {
  const context = useContext(ActiveCallContext);
  if (!context) throw new Error('useActiveCall must be used within an ActiveCallProvider');
  return context;
}

/** Whether the phone at the other end is ringing, switched off, or already on a call. */
type Reach = boolean | 'busy';

/**
 * What to call the wait. A phone that is switched off is not ringing, and
 * telling the caller it is leaves them wondering why nobody picks up.
 */
function waitingLabel(reachable: Reach | null): string {
  if (reachable === 'busy') return 'In another call or meeting';
  if (reachable === true) return 'Ringing\u2026';
  if (reachable === false) return 'Calling\u2026';
  return 'Waiting for others';
}

/** How long somebody may sit alone before being asked whether they meant to. */
const ALONE_BEFORE_ASKING_MS = 90_000;
/** How long they have to answer before the call is dropped. */
const ANSWER_WINDOW_MS = 30_000;

/** How long the caller hears it ring: as long as the other end rings (IncomingCallDialog). */
const RINGBACK_FOR_MS = 45_000;


/**
 * A sound and a toast when somebody else comes in. Not for you, and not for
 * everybody already there when you connect (see joinPing). Arrivals close together
 * are one announcement rather than a burst of sounds. If the browser will not play
 * the sound yet, the toast still says it.
 */
function useJoinAnnouncements() {
  const room = useRoomContext();
  const toast = useToast();
  const { joined: chime, stop: closeChime } = useChime();
  const { notify } = useDesktopNotifications();
  // A new function on every render, and resubscribing would forget who is here.
  const toastRef = useRef(toast.toast);
  toastRef.current = toast.toast;

  useEffect(() => {
    const tracker = createJoinTracker();
    let pending: string[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;

    const flush = () => {
      const message = joinedMessage(pending);
      pending = [];
      chime();
      toastRef.current(message);
      if (document.visibilityState !== 'visible') notify({ title: 'TeamUp call', body: message, tag: 'call-joined' });
    };
    const presentNow = () => [...room.remoteParticipants.values()].map((p) => p.identity);
    const onConnected = () => tracker.connected(presentNow(), Date.now());
    const onJoined = (participant: { identity: string; name?: string }) => {
      if (!tracker.joined(participant.identity, Date.now())) return;
      pending.push(participant.name || participant.identity);
      clearTimeout(timer);
      timer = setTimeout(flush, BURST_WINDOW_MS);
    };
    const onLeft = (participant: { identity: string }) => tracker.left(participant.identity, Date.now());

    if (room.state === ConnectionState.Connected) onConnected();
    room.on(RoomEvent.Connected, onConnected);
    room.on(RoomEvent.Reconnected, onConnected);
    room.on(RoomEvent.ParticipantConnected, onJoined);
    room.on(RoomEvent.ParticipantDisconnected, onLeft);
    return () => {
      clearTimeout(timer);
      room.off(RoomEvent.Connected, onConnected);
      room.off(RoomEvent.Reconnected, onConnected);
      room.off(RoomEvent.ParticipantConnected, onJoined);
      room.off(RoomEvent.ParticipantDisconnected, onLeft);
      // Its audio context otherwise runs until the tab closes, one for each call.
      closeChime();
    };
  }, [room, chime, closeChime, notify]);
}

/**
 * A browser may refuse to play a call's sound on a page nobody has touched, which is
 * how a page arrived at from a link starts. The call then shows people who cannot be
 * heard, and nothing else would retry: only a click on this does.
 */
export function BlockedAudioPrompt({ compact }: { compact: boolean }) {
  const room = useRoomContext();
  const { canPlayAudio, startAudio } = useAudioPlayback(room);
  if (canPlayAudio) return null;
  return (
    <div
      role="alert"
      className={cn(
        'flex shrink-0 items-center justify-center gap-2 border-b border-border bg-muted text-sm',
        compact ? 'px-2 py-1' : 'flex-wrap px-3 py-2'
      )}
    >
      {!compact && <span>Your browser has not started this call's sound.</span>}
      <Button size="sm" className="h-7" onClick={() => void startAudio()}>
        <Volume2 className="mr-1 h-4 w-4" aria-hidden="true" />
        Turn on sound
      </Button>
    </div>
  );
}

/**
 * Keeps toasts above the call's controls while it fills the screen, however tall they
 * are: they wrap on a phone, and gain a note where a browser cannot share a screen.
 */
function useToastsAbove(bar: React.RefObject<HTMLElement>, active: boolean) {
  useEffect(() => {
    const element = bar.current;
    if (!active || !element) return;
    const root = document.documentElement;
    const place = () => root.style.setProperty('--toast-bottom', `${element.offsetHeight}px`);
    place();
    const observer = new ResizeObserver(place);
    observer.observe(element);
    return () => {
      observer.disconnect();
      root.style.removeProperty('--toast-bottom');
    };
  }, [bar, active]);
}

/**
 * Holds the call for as long as you are in it.
 *
 * The room used to live on the call page, so navigating anywhere in the app
 * unmounted it and dropped the call. It lives here instead, above the router:
 * the page you are on only decides whether it is shown full size or shrunk into
 * a corner you can drag around.
 */
export function ActiveCallProvider({ children }: { children: ReactNode }) {
  const [activeCall, setActiveCall] = useState<ActiveCall | null>(null);
  // Keyed by call, because the server says whether the other end is there as
  // soon as the call goes out - which is before joining has finished, and
  // before there is an active call to attribute it to.
  const [reach, setReach] = useState<Record<string, Reach>>({});
  const [joining, setJoining] = useState(false);
  const [failure, setFailure] = useState<JoinFailure | null>(null);
  const [watchedDoor, watchDoor] = useState<string | null>(null);
  const clearFailure = useCallback(() => setFailure(null), []);
  const toast = useToast();
  const location = useLocation();
  const navigate = useNavigate();
  const { on } = useWebSocket();
  const activeCallIdRef = useRef<string | null>(null);
  activeCallIdRef.current = activeCall?.callId ?? null;
  // The call this tab last set out to be in. It outlives the call, because the room can
  // close on the caller just before the frame saying nobody answered arrives.
  const latestCallIdRef = useRef<string | null>(null);
  // Calls said to be missed before this tab knew it was waiting on them: turned
  // down, or found busy, before the caller's own page had opened the call. On
  // a slow connection the person called can decline before the call page has
  // even appeared, and that word, dropped as being about some other call, left
  // the caller to join a call already over and sit in it until it gave up.
  const missedEarlyRef = useRef(new Map<string, string>());

  const join = useCallback(
    async (callId: string) => {
      setFailure(null);
      latestCallIdRef.current = callId;
      const missedIn = missedEarlyRef.current.get(callId);
      if (missedIn) {
        // Over before it opened here: back to the conversation, which says why.
        missedEarlyRef.current.delete(callId);
        navigate(`/chat/${missedIn}`, { replace: true });
        return;
      }
      if (activeCall?.callId === callId) return;
      setJoining(true);
      try {
        const res = await callsApi.getToken(callId);
        setActiveCall({ callId, token: res.data.token, voice: res.data.callType === 'audio' });
      } catch (err) {
        setFailure(classifyJoinError(err));
      } finally {
        setJoining(false);
      }
    },
    [activeCall?.callId, navigate]
  );

  const enter = useCallback((callId: string, token: string) => {
    setFailure(null);
    latestCallIdRef.current = callId;
    setActiveCall({ callId, token, standing: true });
  }, []);

  const leave = useCallback(() => {
    setActiveCall((current) => {
      // Standing on the call's own page after leaving would rejoin it
      // immediately, because that page's whole job is to join whatever the
      // URL names. Step off it. Where you are now, not where you were when
      // this was made: the room closing can be reported by a handler holding
      // an older copy, and a caller already moved to the conversation (a
      // declined call) was then sent on from it to the calls list.
      if (current && window.location.pathname === `/calls/${current.callId}`) {
        navigate('/calls', { replace: true });
      }
      return null;
    });
    setFailure(null);
  }, [navigate]);

  // "Busy" can come from either of two frames, depending on which arrives
  // first; it is said once per person per call.
  const toldBusyRef = useRef(new Set<string>());
  const sayBusy = useCallback(
    (callId: string, name?: string) => {
      const key = `${callId}/${name ?? ''}`;
      if (toldBusyRef.current.has(key)) return;
      toldBusyRef.current.add(key);
      toast.toast(name ? `${name} is in another call or meeting.` : 'They are in another call or meeting.');
    },
    [toast]
  );

  // Somebody declining, or the call being ended, has to reach whoever started
  // it - otherwise they sit in an empty room with no idea why.
  useEffect(() => {
    return on('call.ended', (msg) => {
      const payload = msg.payload as any;
      if (!payload?.callId || activeCallIdRef.current !== payload.callId) return;
      const who = payload.declinedBy ? `${payload.declinedBy} ` : '';
      // One person not coming does not end the call for everybody else, so this
      // only tells you. When that was the last person who could have come, the
      // server closes the room, and being disconnected is what takes you out.
      if (payload.reason === 'no_answer') {
        toast.toast(who ? `${who}did not answer.` : 'Nobody answered.');
        return;
      }
      if (payload.reason === 'declined') {
        toast.toast(`${who}declined the call.`);
        return;
      }
      // Not rung at all: they are talking to somebody else. Said here when
      // others are still being rung; when they were the only one, the call is
      // over at once, and call.missed below says it.
      if (payload.reason === 'busy') {
        sayBusy(payload.callId, payload.declinedBy);
        return;
      }
      toast.toast('The call has ended.');
      setActiveCall(null);
    });
  }, [on, toast, sayBusy]);

  // A call that did not happen almost always turns into a message, so whoever
  // was calling is put into that conversation rather than left on an empty
  // call page to go and find the person again.
  useEffect(() => {
    return on('call.missed', (msg) => {
      const payload = msg.payload as any;
      if (!payload?.channelId) return;
      // Only somebody still waiting on this call. An earlier one ringing out is no reason
      // to pull them off another call, off joining one, or off whatever they moved on to.
      if (activeCallIdRef.current && activeCallIdRef.current !== payload.callId) return;
      const waitingOnIt = latestCallIdRef.current === payload.callId || window.location.pathname === `/calls/${payload.callId}`;
      if (!waitingOnIt) {
        // Perhaps a call this tab is only now opening; kept for when it does.
        const early = missedEarlyRef.current;
        early.set(payload.callId, payload.channelId);
        if (early.size > 20) early.delete(early.keys().next().value as string);
        // Busy is decided as the call is made, so it is always about one just placed.
        if (payload.reason === 'busy') sayBusy(payload.callId, payload.name);
        return;
      }
      // Busy is known before the call has even opened here, so it is said now.
      if (payload.reason === 'busy') sayBusy(payload.callId, payload.name);
      navigate(`/chat/${payload.channelId}`);
    });
  }, [on, navigate, sayBusy]);

  // Whether the phone at the other end is actually ringing. The server says so
  // when the call goes out and again if they come online while it is still
  // going, which is the difference between "calling" and "ringing".
  useEffect(() => {
    return on('call.reach', (msg) => {
      const payload = msg.payload as any;
      if (!payload?.callId) return;
      setReach((current) => ({ ...current, [payload.callId]: payload.busy ? 'busy' : !!payload.reachable }));
    });
  }, [on]);

  // The sound of a call ending, however it ended: you left, the last other
  // person did, or the connection went. Decoded ahead, so it is not late.
  useEffect(() => preloadSounds(['ringback', 'call-ended']), []);
  const wasInCallRef = useRef(false);
  useEffect(() => {
    if (activeCall) wasInCallRef.current = true;
    else if (wasInCallRef.current) {
      wasInCallRef.current = false;
      playSound('call-ended');
    }
  }, [activeCall]);

  // Being in a call is observed rather than chosen, so it sets itself and
  // clears when the call ends however it ends.
  useEffect(() => {
    if (!activeCall) return;
    presenceApi.update('in_call').catch(() => {
      // Cosmetic; not worth interrupting the call for.
    });
    return () => {
      presenceApi.update('online').catch(() => {});
    };
  }, [activeCall]);

  // Stable, because LiveKitRoom's effect that connects has this among its
  // dependencies: a new function on each render would connect again, which after
  // the room has closed would quietly start a new room.
  const toastError = useRef(toast.error);
  toastError.current = toast.error;
  const onRoomError = useCallback((err: Error) => {
    toastError.current(err.message || 'Lost connection to the call.');
  }, []);

  // Full size on that call's own page; a corner window everywhere else.
  const expanded = !!activeCall && location.pathname === `/calls/${activeCall.callId}`;

  const value = useMemo(
    () => ({ activeCall, join, enter, leave, joining, failure, clearFailure, watchedDoor, watchDoor }),
    [activeCall, join, enter, leave, joining, failure, clearFailure, watchedDoor]
  );

  return (
    <ActiveCallContext.Provider value={value}>
      {children}
      {activeCall && (
        <LiveKitRoom
          key={activeCall.callId}
          token={activeCall.token}
          serverUrl={liveKitURL()}
          connect
          // Joining a meeting or a video call with the camera and microphone
          // off: you turn them on when you are ready, rather than finding out
          // you were already on. A call is a conversation, so its microphone is
          // on from the start; the camera is still yours to turn on.
          video={false}
          audio={!!activeCall.voice}
          data-lk-theme="default"
          onDisconnected={leave}
          onError={onRoomError}
          // The room element is never unmounted by navigation; only its frame
          // changes, so the media session survives moving around the app.
          className="contents"
        >
          <CallFrame
            callId={activeCall.callId}
            expanded={expanded}
            onLeave={leave}
            reachable={activeCall.callId in reach ? reach[activeCall.callId] : null}
          />
          <RoomAudioRenderer />
          <CameraBackground />
        </LiveKitRoom>
      )}
    </ActiveCallContext.Provider>
  );
}

/** The window the call sits in, and everything that depends on being inside it. */
function CallFrame({
  callId,
  expanded,
  onLeave,
  reachable,
}: {
  callId: string;
  expanded: boolean;
  onLeave: () => void;
  /** Whether the other end has a session open; null until the server says. */
  reachable: Reach | null;
}) {
  const navigate = useNavigate();
  const toast = useToast();
  const room = useRoomContext();
  const remote = useRemoteParticipants();
  const alone = remote.length === 0;
  const sidebarOpen = useUIStore((s) => s.sidebarOpen);
  useJoinAnnouncements();

  const { data: details } = useQuery({
    queryKey: ['call', callId],
    queryFn: () => callsApi.get(callId).then((res) => res.data),
    staleTime: 60_000,
  });
  const title: string | undefined = details?.title?.trim() || undefined;
  // Everybody may be in a standing meeting's room, and most of them may not read its
  // log, so nobody is offered a button whose only answer is a refusal.
  const canReadLog = details?.canReadLog === true;
  const chatTransport = useMemo<ChatTransport>(
    () => ({
      load: async () => (await callsApi.chat(callId)).data.messages,
      send: async (content) => (await callsApi.sendChat(callId, content)).data,
    }),
    [callId]
  );

  // Above the bottom navigation on a phone, where the corner would cover it.
  const [frame, setFrame] = useState(() =>
    fitCallWindow(savedSize(), { x: 8, y: window.innerWidth < 1024 ? 72 : 24 }, viewport())
  );
  const { size, position } = frame;
  const [asking, setAsking] = useState(false);
  const [showingLog, setShowingLog] = useState(false);
  const [showingGuests, setShowingGuests] = useState(false);
  // What a drag or a resize started from: where the pointer was, and the frame.
  const gesture = useRef<{ kind: 'move' | 'resize'; startX: number; startY: number; from: typeof frame } | null>(null);

  // Turning a phone, or narrowing a browser, must not leave the window off screen.
  useEffect(() => {
    const refit = () => setFrame((f) => fitCallWindow(f.size, f.position, viewport()));
    window.addEventListener('resize', refit);
    return () => window.removeEventListener('resize', refit);
  }, []);

  // Leaving is always just leaving. Anybody still in the call carries on, and
  // the call itself is closed only by whoever turns out to be last out - there
  // is deliberately no way to hang up on everybody else.
  const hangUp = useCallback(async () => {
    const lastOut = remote.length === 0;
    try {
      if (lastOut) {
        // Nobody is left, so the call is over rather than merely empty. Left
        // open it would sit "active" in everybody's history until the room
        // timed out.
        await callsApi.end(callId).catch(() => {
          // The room's own timeout closes it if this does not.
        });
      }
      await room.disconnect();
    } finally {
      onLeave();
    }
  }, [room, onLeave, remote.length, callId]);

  // While their phone rings, the caller hears it ring, as on a phone: only when
  // it really is ringing ("Ringing\u2026"), and only until somebody comes in.
  const ringing = alone && reachable === true;
  useEffect(() => {
    if (!ringing) return;
    const stop = playSound('ringback', { loop: true });
    const giveUp = setTimeout(stop, RINGBACK_FOR_MS);
    return () => {
      clearTimeout(giveUp);
      stop();
    };
  }, [ringing]);

  // Sitting alone is usually a call that is already over. Ask, and if there is
  // no answer, drop it rather than leaving a room open indefinitely.
  useEffect(() => {
    if (!alone) {
      setAsking(false);
      return;
    }
    const ask = setTimeout(() => setAsking(true), ALONE_BEFORE_ASKING_MS);
    return () => clearTimeout(ask);
  }, [alone]);

  useEffect(() => {
    if (!asking) return;
    const drop = setTimeout(() => {
      toast.toast('You were the only one here, so the call was ended.');
      void hangUp();
    }, ANSWER_WINDOW_MS);
    return () => clearTimeout(drop);
  }, [asking, hangUp, toast]);

  // Moving and resizing the minimised window. Pointer events cover mouse and
  // finger alike, and capture keeps it following the pointer outside the window.
  // It moves from anywhere on it that is not a button, which on a phone is
  // what a finger expects; it is resized from the grip in its top left corner,
  // the one away from the corner it is anchored to.
  const startGesture = (kind: 'move' | 'resize') => (e: React.PointerEvent) => {
    if (expanded || e.button > 0) return;
    // Capturing the pointer sends every later event here, so a drag started on
    // top of a button swallowed its click entirely - expand and leave did nothing.
    if (kind === 'move' && (e.target as HTMLElement).closest('button')) return;
    e.stopPropagation();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    gesture.current = { kind, startX: e.clientX, startY: e.clientY, from: frame };
  };

  const onGesture = (e: React.PointerEvent) => {
    const g = gesture.current;
    if (!g) return;
    const dx = e.clientX - g.startX;
    const dy = e.clientY - g.startY;
    setFrame(
      g.kind === 'move'
        ? fitCallWindow(g.from.size, { x: g.from.position.x - dx, y: g.from.position.y - dy }, viewport())
        : fitCallWindow({ width: g.from.size.width - dx, height: g.from.size.height - dy }, g.from.position, viewport())
    );
  };

  const endGesture = (e: React.PointerEvent) => {
    const g = gesture.current;
    if (!g) return;
    gesture.current = null;
    (e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);
    if (g.kind === 'resize') keepSize(frame.size);
  };

  // The grip answers the arrow keys too, for whoever is not using a pointer.
  const resizeByKey = (e: React.KeyboardEvent) => {
    const step = { ArrowLeft: [1, 0], ArrowRight: [-1, 0], ArrowUp: [0, 1], ArrowDown: [0, -1] }[e.key];
    if (!step) return;
    e.preventDefault();
    const next = fitCallWindow(
      { width: size.width + step[0] * RESIZE_STEP, height: size.height + step[1] * RESIZE_STEP },
      position,
      viewport()
    );
    setFrame(next);
    keepSize(next.size);
  };

  return (
    <MeetingChatProvider transport={chatTransport}>
      <div
        // text-foreground because LiveKit's theme sets white text on everything
        // inside it, which on a light page made this bar's words and its minimise
        // button invisible.
        className={cn(
          'z-50 overflow-hidden bg-background text-foreground shadow-2xl',
          expanded
            ? cn('fixed inset-0', sidebarOpen ? 'lg:left-64' : 'lg:left-16')
            : // touch-none: a finger on it moves the window rather than scrolling the page.
              'fixed touch-none select-none rounded-xl border border-border'
        )}
        style={
          expanded
            ? undefined
            : {
                right: position.x,
                bottom: position.y,
                width: size.width,
                height: size.height,
              }
        }
        data-testid={expanded ? undefined : 'call-window'}
        onPointerDown={startGesture('move')}
        onPointerMove={onGesture}
        onPointerUp={endGesture}
        onPointerCancel={endGesture}
      >
        {!expanded && (
          <button
            type="button"
            aria-label="Resize the call window"
            title="Drag to resize"
            className="absolute left-1 top-1.5 z-10 flex h-7 w-7 cursor-nwse-resize items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [@media(pointer:coarse)]:h-8 [@media(pointer:coarse)]:w-8"
            onPointerDown={startGesture('resize')}
            onPointerMove={onGesture}
            onPointerUp={endGesture}
            onPointerCancel={endGesture}
            onKeyDown={resizeByKey}
          >
            <MoveDiagonal2 className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        )}
        <div className="flex h-full flex-col">
          <div
            className={cn(
              'flex items-center gap-2 border-b border-border px-2 py-1.5',
              !expanded && 'cursor-grab pl-9 active:cursor-grabbing'
            )}
          >
            <span className="min-w-0 flex-1 truncate text-sm font-medium">
              {expanded && title ? `${title} \u00b7 ` : ''}
              {alone ? waitingLabel(reachable) : `${compactCount(remote.length + 1, expanded)}`}
            </span>
            {expanded ? (
              <>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  aria-label="Copy the link to this call"
                  title="Copy link"
                  onClick={async () => {
                    if (await copyCallLink(callId)) toast.success('Link copied. Anybody not on the call will have to ask to join.');
                    else toast.error('Could not copy. Your browser did not allow it.');
                  }}
                >
                  <Link2 className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  aria-label="Guests"
                  title="Guests"
                  onClick={() => setShowingGuests(true)}
                >
                  <UserPlus className="h-4 w-4" />
                </Button>
                {canReadLog && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    aria-label="Show the call log"
                    title="Call log"
                    onClick={() => setShowingLog(true)}
                  >
                    <History className="h-4 w-4" />
                  </Button>
                )}
              </>
            ) : (
              <CompactCallControls />
            )}
            <Button
              variant="ghost"
              size="icon"
              className="relative h-7 w-7"
              aria-label={expanded ? 'Minimise the call' : 'Expand the call'}
              onClick={() => navigate(expanded ? '/chat' : `/calls/${callId}`)}
            >
              {expanded ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
              {!expanded && <UnreadChatMark />}
            </Button>
            <Button
              variant="destructive"
              size="icon"
              className="h-7 w-7"
              aria-label="Leave the call"
              onClick={hangUp}
            >
              <PhoneOff className="h-4 w-4" />
            </Button>
          </div>

          <BlockedAudioPrompt compact={!expanded} />

          <div className="min-h-0 flex-1">
            <CallTiles callId={callId} compact={!expanded} />
          </div>
        </div>
      </div>

      <CallLogDialog callId={callId} open={showingLog} onOpenChange={setShowingLog} live />

      <Dialog open={showingGuests} onOpenChange={setShowingGuests}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Guests</DialogTitle>
            <DialogDescription>People from outside the organization, let in with a link.</DialogDescription>
          </DialogHeader>
          <GuestAccess callId={callId} title={title} />
        </DialogContent>
      </Dialog>

      <Dialog open={asking} onOpenChange={(open) => !open && setAsking(false)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Still there?</DialogTitle>
            <DialogDescription>
              You have been the only one in this call for a while. It will end shortly
              unless you want to keep waiting.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2 sm:justify-center">
            <Button variant="outline" onClick={hangUp}>
              Leave the call
            </Button>
            <Button onClick={() => setAsking(false)}>Stay</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </MeetingChatProvider>
  );
}

/** The corner window is short of room; the full view can say what it is. */
function compactCount(people: number, expanded: boolean) {
  return expanded ? `In a call \u00b7 ${people}` : `${people} in call`;
}

/** The video itself, plus the controls when there is room for them. */
function CallTiles({ callId, compact }: { callId: string; compact: boolean }) {
  const room = useRoomContext();
  const [mediaError, setMediaError] = useState('');
  const controls = useRef<HTMLDivElement>(null);
  useToastsAbove(controls, !compact);

  // Camera and microphone permission is the most common thing to go wrong;
  // an empty tile with no explanation is the worst possible answer.
  useEffect(() => {
    const onFailure = () => {
      setMediaError(
        'TeamUp could not use your camera or microphone. Check the permission in your browser and rejoin.'
      );
    };
    room.on(RoomEvent.MediaDevicesError, onFailure);
    return () => {
      room.off(RoomEvent.MediaDevicesError, onFailure);
    };
  }, [room]);

  return (
    <div className="flex h-full flex-col">
      {mediaError && !compact && (
        <div className="flex items-start gap-3 border-b border-border bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
          <span>{mediaError}</span>
        </div>
      )}

      {!compact && <NotesBanner />}

      <div className="min-h-0 flex-1">
        {compact ? (
          <CallStage callId={callId} compact />
        ) : (
          <StageWithChat>
            <CallStage callId={callId} compact={false} />
          </StageWithChat>
        )}
      </div>

      {/* The controls need room; the corner window keeps only leave, which
          lives in its title bar. */}
      {!compact && (
        <div ref={controls} className="flex shrink-0 flex-col items-center gap-1 border-t border-border p-2 sm:p-3">
          <CallControls>
            <MeetingChatButton />
          </CallControls>
          <ShareUnavailableNote />
        </div>
      )}
    </div>
  );
}

/**
 * Where LiveKit's signalling socket lives.
 *
 * Over HTTPS it has to be reached through this origin: a secure page cannot
 * open a plain ws:// connection, and LiveKit's own port is not behind the
 * site's certificate. Over plain HTTP - a machine on a local network, say -
 * the port is addressed directly, because there is no proxy in front of it.
 */
export function liveKitURL() {
  if (import.meta.env.VITE_LIVEKIT_URL) return import.meta.env.VITE_LIVEKIT_URL;
  if (window.location.protocol === 'https:') {
    return `wss://${window.location.host}/livekit`;
  }
  return `ws://${window.location.hostname}:7880`;
}
