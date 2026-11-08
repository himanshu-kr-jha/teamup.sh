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
import { NotesBanner } from './notetaker';
import { CallControls, CompactCallControls, ShareUnavailableNote } from './CallControls';
import { CallLogDialog } from './CallLogDialog';
import { copyCallLink } from './copyLink';
import { classifyJoinError, type JoinFailure } from './knock';
import { BURST_WINDOW_MS, createJoinTracker, joinedMessage } from './joinPing';
import {
  AlertTriangle,
  GripVertical,
  History,
  Link2,
  Maximize2,
  Minimize2,
  PhoneOff,
  Volume2,
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
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';
import { useToast } from '@/shared/hooks/useToast';
import { useUIStore } from '@/shared/store';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { cn } from '@/shared/utils/cn';

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

/**
 * What to call the wait. A phone that is switched off is not ringing, and
 * telling the caller it is leaves them wondering why nobody picks up.
 */
function waitingLabel(reachable: boolean | null): string {
  if (reachable === true) return 'Ringing\u2026';
  if (reachable === false) return 'Calling\u2026';
  return 'Waiting for others';
}

/** How long somebody may sit alone before being asked whether they meant to. */
const ALONE_BEFORE_ASKING_MS = 90_000;
/** How long they have to answer before the call is dropped. */
const ANSWER_WINDOW_MS = 30_000;

const COMPACT_WIDTH = 320;
const COMPACT_HEIGHT = 200;

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
function BlockedAudioPrompt({ compact }: { compact: boolean }) {
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
  const [reach, setReach] = useState<Record<string, boolean>>({});
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

  const join = useCallback(
    async (callId: string) => {
      setFailure(null);
      latestCallIdRef.current = callId;
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
    [activeCall?.callId]
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
      // URL names. Step off it.
      if (current && location.pathname === `/calls/${current.callId}`) {
        navigate('/calls', { replace: true });
      }
      return null;
    });
    setFailure(null);
  }, [location.pathname, navigate]);

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
      toast.toast('The call has ended.');
      setActiveCall(null);
    });
  }, [on, toast]);

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
      if (waitingOnIt) navigate(`/chat/${payload.channelId}`);
    });
  }, [on, navigate]);

  // Whether the phone at the other end is actually ringing. The server says so
  // when the call goes out and again if they come online while it is still
  // going, which is the difference between "calling" and "ringing".
  useEffect(() => {
    return on('call.reach', (msg) => {
      const payload = msg.payload as any;
      if (!payload?.callId) return;
      setReach((current) => ({ ...current, [payload.callId]: !!payload.reachable }));
    });
  }, [on]);

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
  reachable: boolean | null;
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

  // Above the bottom navigation on a phone, where the corner would cover it.
  const [position, setPosition] = useState(() => ({ x: 8, y: window.innerWidth < 1024 ? 72 : 24 }));
  const [asking, setAsking] = useState(false);
  const [showingLog, setShowingLog] = useState(false);
  const dragState = useRef<{ startX: number; startY: number; originX: number; originY: number } | null>(null);

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

  // Dragging the minimised window. Pointer events cover mouse and touch, and
  // capture keeps it following the pointer outside the window.
  const startDrag = (e: React.PointerEvent) => {
    if (expanded) return;
    // Capturing the pointer sends every later event to the title bar, so a
    // drag started on top of a button swallowed its click entirely - expand
    // and leave did nothing.
    if ((e.target as HTMLElement).closest('button')) return;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    dragState.current = {
      startX: e.clientX,
      startY: e.clientY,
      originX: position.x,
      originY: position.y,
    };
  };

  const onDrag = (e: React.PointerEvent) => {
    const state = dragState.current;
    if (!state) return;
    const width = Math.min(COMPACT_WIDTH, window.innerWidth - 16);
    const height = COMPACT_HEIGHT;
    setPosition({
      // Kept on screen: a window dragged off the edge cannot be got back.
      x: clamp(state.originX - (e.clientX - state.startX), 8, window.innerWidth - width),
      y: clamp(state.originY - (e.clientY - state.startY), 8, window.innerHeight - height),
    });
  };

  const endDrag = (e: React.PointerEvent) => {
    dragState.current = null;
    (e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);
  };

  return (
    <>
      <div
        // text-foreground because LiveKit's theme sets white text on everything
        // inside it, which on a light page made this bar's words and its minimise
        // button invisible.
        className={cn(
          'z-50 overflow-hidden bg-background text-foreground shadow-2xl',
          expanded
            ? cn('fixed inset-0', sidebarOpen ? 'lg:left-64' : 'lg:left-16')
            : 'fixed rounded-xl border border-border'
        )}
        style={
          expanded
            ? undefined
            : {
                right: position.x,
                bottom: position.y,
                width: `min(${COMPACT_WIDTH}px, calc(100vw - 16px))`,
                height: COMPACT_HEIGHT,
              }
        }
      >
        <div className="flex h-full flex-col">
          <div
            className={cn(
              'flex items-center gap-2 border-b border-border px-2 py-1.5',
              !expanded && 'cursor-grab active:cursor-grabbing'
            )}
            onPointerDown={startDrag}
            onPointerMove={onDrag}
            onPointerUp={endDrag}
          >
            {!expanded && <GripVertical className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
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
              className="h-7 w-7"
              aria-label={expanded ? 'Minimise the call' : 'Expand the call'}
              onClick={() => navigate(expanded ? '/chat' : `/calls/${callId}`)}
            >
              {expanded ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
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
    </>
  );
}

/** The corner window is short of room; the full view can say what it is. */
function compactCount(people: number, expanded: boolean) {
  return expanded ? `In a call \u00b7 ${people}` : `${people} in call`;
}

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), Math.max(min, max));
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
        <CallStage callId={callId} compact={compact} />
      </div>

      {/* The controls need room; the corner window keeps only leave, which
          lives in its title bar. */}
      {!compact && (
        <div ref={controls} className="flex shrink-0 flex-col items-center gap-1 border-t border-border p-2 sm:p-3">
          <CallControls />
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
function liveKitURL() {
  if (import.meta.env.VITE_LIVEKIT_URL) return import.meta.env.VITE_LIVEKIT_URL;
  if (window.location.protocol === 'https:') {
    return `wss://${window.location.host}/livekit`;
  }
  return `ws://${window.location.hostname}:7880`;
}
