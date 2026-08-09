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
  ControlBar,
  GridLayout,
  LiveKitRoom,
  ParticipantTile,
  RoomAudioRenderer,
  useRemoteParticipants,
  useRoomContext,
  useTracks,
} from '@livekit/components-react';
import { RoomEvent, Track } from 'livekit-client';
import { AlertTriangle, GripVertical, Maximize2, Minimize2, PhoneOff } from 'lucide-react';
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
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { cn } from '@/shared/utils/cn';

interface ActiveCall {
  callId: string;
  token: string;
}

interface ActiveCallContextValue {
  activeCall: ActiveCall | null;
  /** Joins a call, or does nothing if already in it. */
  join: (callId: string) => Promise<void>;
  /** Leaves without ending it for anybody else. */
  leave: () => void;
  joining: boolean;
  error: string;
}

const ActiveCallContext = createContext<ActiveCallContextValue | undefined>(undefined);

export function useActiveCall() {
  const context = useContext(ActiveCallContext);
  if (!context) throw new Error('useActiveCall must be used within an ActiveCallProvider');
  return context;
}

/** How long somebody may sit alone before being asked whether they meant to. */
const ALONE_BEFORE_ASKING_MS = 90_000;
/** How long they have to answer before the call is dropped. */
const ANSWER_WINDOW_MS = 30_000;

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
  const [joining, setJoining] = useState(false);
  const [error, setError] = useState('');
  const toast = useToast();
  const location = useLocation();
  const navigate = useNavigate();
  const { on } = useWebSocket();

  const join = useCallback(
    async (callId: string) => {
      setError('');
      if (activeCall?.callId === callId) return;
      setJoining(true);
      try {
        const res = await callsApi.getToken(callId);
        setActiveCall({ callId, token: res.data.token });
      } catch (err) {
        setError(apiErrorMessage(err, 'Could not join this call.'));
      } finally {
        setJoining(false);
      }
    },
    [activeCall?.callId]
  );

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
    setError('');
  }, [location.pathname, navigate]);

  // Somebody declining, or the call being ended, has to reach whoever started
  // it — otherwise they sit in an empty room with no idea why.
  useEffect(() => {
    return on('call.ended', (msg) => {
      const payload = msg.payload as any;
      if (!payload?.callId) return;
      setActiveCall((current) => {
        if (current?.callId !== payload.callId) return current;
        const who = payload.declinedBy ? `${payload.declinedBy} ` : '';
        if (payload.reason === 'no_answer') {
          toast.toast(who ? `${who}did not answer.` : 'Nobody answered.');
        } else if (payload.reason === 'declined') {
          toast.toast(`${who}declined the call.`);
        } else {
          toast.toast('The call has ended.');
        }
        return null;
      });
    });
  }, [on, toast]);

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

  // Full size on that call's own page; a corner window everywhere else.
  const expanded = !!activeCall && location.pathname === `/calls/${activeCall.callId}`;

  const value = useMemo(
    () => ({ activeCall, join, leave, joining, error }),
    [activeCall, join, leave, joining, error]
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
          video
          audio
          data-lk-theme="default"
          onDisconnected={leave}
          onError={(err) => toast.error(err.message || 'Lost connection to the call.')}
          // The room element is never unmounted by navigation; only its frame
          // changes, so the media session survives moving around the app.
          className="contents"
        >
          <CallFrame callId={activeCall.callId} expanded={expanded} onLeave={leave} />
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
}: {
  callId: string;
  expanded: boolean;
  onLeave: () => void;
}) {
  const navigate = useNavigate();
  const toast = useToast();
  const room = useRoomContext();
  const remote = useRemoteParticipants();
  const alone = remote.length === 0;

  const [position, setPosition] = useState({ x: 24, y: 24 });
  const [asking, setAsking] = useState(false);
  const dragState = useRef<{ startX: number; startY: number; originX: number; originY: number } | null>(null);

  // Leaving is always just leaving. Anybody still in the call carries on, and
  // the call itself is closed only by whoever turns out to be last out — there
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
    // drag started on top of a button swallowed its click entirely — expand
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
    const width = 320;
    const height = 200;
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
        className={cn(
          'z-50 overflow-hidden bg-background shadow-2xl',
          expanded
            ? 'fixed inset-0 lg:left-64'
            : 'fixed h-[200px] w-[320px] rounded-xl border border-border'
        )}
        style={expanded ? undefined : { right: position.x, bottom: position.y }}
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
            <span className="flex-1 truncate text-sm font-medium">
              {alone ? 'Waiting for others' : `In a call · ${remote.length + 1}`}
            </span>
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

          <div className="min-h-0 flex-1">
            <CallTiles compact={!expanded} />
          </div>
        </div>
      </div>

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

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

/** The video itself, plus the controls when there is room for them. */
function CallTiles({ compact }: { compact: boolean }) {
  const room = useRoomContext();
  const remote = useRemoteParticipants();
  const [mediaError, setMediaError] = useState('');

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

  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: false }
  );

  return (
    <div className="flex h-full flex-col">
      {mediaError && !compact && (
        <div className="flex items-start gap-3 border-b border-border bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
          <span>{mediaError}</span>
        </div>
      )}

      <div className="relative min-h-0 flex-1">
        <GridLayout tracks={tracks} className="h-full">
          <ParticipantTile />
        </GridLayout>
        {remote.length === 0 && (
          <div className="pointer-events-none absolute inset-x-0 bottom-2 text-center text-xs text-muted-foreground">
            Waiting for someone to join…
          </div>
        )}
      </div>

      {/* The controls need room; the corner window keeps only leave, which
          lives in its title bar. */}
      {!compact && (
        <div className="flex items-center justify-center border-t border-border p-3">
          <ControlBar variation="minimal" controls={{ leave: false }} />
        </div>
      )}
    </div>
  );
}

/** Same host as the page unless told otherwise, so this works behind a proxy. */
function liveKitURL() {
  if (import.meta.env.VITE_LIVEKIT_URL) return import.meta.env.VITE_LIVEKIT_URL;
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${window.location.hostname}:7880`;
}
