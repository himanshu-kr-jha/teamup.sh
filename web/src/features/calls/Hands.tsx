import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useRoomContext } from '@livekit/components-react';
import { ConnectionState, RoomEvent } from 'livekit-client';
import { useQuery } from '@tanstack/react-query';
import { ChevronDown, Hand } from 'lucide-react';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useChime } from '@/shared/hooks/useChime';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { shortcutHint } from '@/shared/shortcuts/catalogue';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import {
  AUTO_LOWERED_MESSAGE,
  AUTO_LOWER_FAILED_MESSAGE,
  HAND_ATTRIBUTE,
  LOWERED_FOR_YOU_MESSAGE,
  LOWER_RETRY_DELAYS_MS,
  NOT_RESTORED_MESSAGE,
  PROOF_REFRESH_MS,
  RESTORED_AS_NEW_MESSAGE,
  createAutoLower,
  handQueue,
  handsLabel,
  needsRestore,
  newlyRaised,
  raisedMessage,
  type RaisedHand,
} from './handQueue';

/**
 * Raised hands in a call. The hand itself lives on the media server, as a
 * participant attribute only TeamUp's server writes (internal/calls/hand.go), so
 * this only reads the room and asks the server to change it.
 */
interface HandsValue {
  queue: RaisedHand[];
  /** Your own hand, if it is up. */
  mine: RaisedHand | null;
  /** A member can raise a hand; a guest has no account to ask the server with. */
  canRaise: boolean;
  /** The organizer, or an admin, may lower other people's. */
  canLowerOthers: boolean;
  pending: boolean;
  toggle: () => void;
  lower: (identity: string) => void;
  lowerAll: () => void;
}

const HandsContext = createContext<HandsValue | null>(null);

export function useHands(): HandsValue | null {
  return useContext(HandsContext);
}

/** How often your own speaking is checked while your hand is up. */
const AUTO_LOWER_TICK_MS = 250;

/** How long after the connection comes back to look at your own hand: its attributes arrive with it. */
const SETTLE_AFTER_RECONNECT_MS = 500;

/** Your own hand as this tab last saw it up, and the server's proof of its place. */
interface Kept {
  raisedAt: string;
  proof?: string;
}

export function HandsProvider({ callId, asGuest = false, children }: { callId: string; asGuest?: boolean; children: ReactNode }) {
  const room = useRoomContext();
  const toast = useToast();
  const toastRef = useRef(toast);
  toastRef.current = toast;
  const { hand: chime, stop: closeChime } = useChime();

  // The same question CallStage asks, and so the same answer from the cache: the
  // organizer or an admin is who manages a meeting.
  const { data: guestLink } = useQuery({
    queryKey: ['guest-link', callId],
    queryFn: async () => (await callsApi.guestLink(callId)).data,
    enabled: !asGuest && !!callId,
  });
  const canLowerOthers = !asGuest && !!guestLink?.canManage;

  const read = useCallback(
    () => handQueue([room.localParticipant, ...room.remoteParticipants.values()]),
    [room]
  );
  const [queue, setQueue] = useState<RaisedHand[]>(read);
  const [pending, setPending] = useState(false);
  // Why your own hand is coming down, so its coming down can be explained.
  const lowering = useRef<'self' | 'auto' | null>(null);
  // Your hand while it is up, kept through a lost connection so it can be put back.
  const kept = useRef<Kept | null>(null);

  const restore = useCallback(async () => {
    const was = kept.current;
    if (!was || asGuest) return;
    try {
      const res = await callsApi.setHand(callId, true, was.proof ? { restore: { raisedAt: was.raisedAt, proof: was.proof } } : {});
      kept.current = res.data.raisedAt ? { raisedAt: res.data.raisedAt, proof: res.data.proof } : null;
      if (!res.data.restored) toastRef.current.toast(RESTORED_AS_NEW_MESSAGE);
    } catch {
      kept.current = null;
      toastRef.current.toast(NOT_RESTORED_MESSAGE);
    }
  }, [asGuest, callId]);

  useEffect(() => {
    let before = read();
    // Hands already up when you arrive are not news.
    let primed = room.state === ConnectionState.Connected;
    let settle: ReturnType<typeof setTimeout> | undefined;
    setQueue(before);

    const ownAttribute = () => room.localParticipant.attributes?.[HAND_ATTRIBUTE];

    const look = () => {
      const after = read();
      const me = room.localParticipant.identity;
      // While the connection is being made again, people and hands come and go
      // as the room is rebuilt: none of that is news.
      const connected = room.state === ConnectionState.Connected;
      if (primed && connected) {
        const raised = newlyRaised(before, after).filter((h) => h.identity !== me);
        raised.forEach((h) => toastRef.current.toast(raisedMessage(h.name)));
        if (raised.length > 0) chime();
      }
      const wasUp = before.some((h) => h.identity === me);
      const isUp = after.some((h) => h.identity === me);
      if (isUp) {
        const at = ownAttribute() ?? '';
        if (kept.current?.raisedAt !== at) kept.current = { raisedAt: at };
      } else if (wasUp && connected) {
        // Taken down while connected: by you, by speaking, or by the organizer.
        if (primed) {
          if (lowering.current === 'auto') toastRef.current.toast(AUTO_LOWERED_MESSAGE);
          else if (lowering.current === null) toastRef.current.toast(LOWERED_FOR_YOU_MESSAGE);
        }
        lowering.current = null;
        kept.current = null;
      }
      before = after;
      setQueue(after);
    };
    const connected = () => {
      before = read();
      primed = true;
      setQueue(before);
    };
    // A connection made again from scratch starts with no attributes if the room
    // was lost with it: put your hand back if it went.
    const reconnected = () => {
      connected();
      clearTimeout(settle);
      settle = setTimeout(() => {
        if (needsRestore(kept.current, ownAttribute())) void restore();
      }, SETTLE_AFTER_RECONNECT_MS);
    };

    room.on(RoomEvent.ParticipantAttributesChanged, look);
    room.on(RoomEvent.ParticipantConnected, look);
    room.on(RoomEvent.ParticipantDisconnected, look);
    room.on(RoomEvent.Connected, connected);
    room.on(RoomEvent.Reconnected, reconnected);
    return () => {
      clearTimeout(settle);
      room.off(RoomEvent.ParticipantAttributesChanged, look);
      room.off(RoomEvent.ParticipantConnected, look);
      room.off(RoomEvent.ParticipantDisconnected, look);
      room.off(RoomEvent.Connected, connected);
      room.off(RoomEvent.Reconnected, reconnected);
    };
  }, [room, read, chime, restore]);

  // Its audio context otherwise runs until the tab closes.
  useEffect(() => closeChime, [closeChime]);

  const me = room.localParticipant.identity;
  const mine = queue.find((h) => h.identity === me) ?? null;

  /** Asks the server; true if it was done. Says nothing either way. */
  const send = useCallback(
    async (raised: boolean, identity?: string): Promise<boolean> => {
      const own = !identity || identity === room.localParticipant.identity;
      try {
        const res = await callsApi.setHand(callId, raised, own ? {} : { userId: identity });
        if (own && res.data.raised && res.data.raisedAt) kept.current = { raisedAt: res.data.raisedAt, proof: res.data.proof };
        return true;
      } catch {
        return false;
      }
    },
    [callId, room]
  );

  /** What a click asks for, with a word if it could not be done. */
  const change = useCallback(
    async (raised: boolean, identity?: string) => {
      const own = !identity || identity === room.localParticipant.identity;
      if (own) {
        setPending(true);
        if (!raised) lowering.current = 'self';
      }
      try {
        const res = await callsApi.setHand(callId, raised, own ? {} : { userId: identity });
        if (own && res.data.raised && res.data.raisedAt) kept.current = { raisedAt: res.data.raisedAt, proof: res.data.proof };
      } catch (err) {
        if (own && !raised) lowering.current = null;
        toastRef.current.error(
          apiErrorMessage(err, raised ? 'Could not raise your hand.' : own ? 'Could not lower your hand.' : 'Could not lower that hand.')
        );
      } finally {
        if (own) setPending(false);
      }
    },
    [callId, room]
  );

  // Your hand comes down by itself once you have been speaking for a while; if
  // the server cannot be reached it tries again, less and less often, and then
  // says so rather than leaving the hand up without a word.
  const upAt = mine?.at;
  useEffect(() => {
    if (upAt === undefined || asGuest) return;
    const auto = createAutoLower();
    let retry: ReturnType<typeof setTimeout> | undefined;
    let cancelled = false;
    const attempt = async (n: number) => {
      lowering.current = 'auto';
      if (await send(false)) return;
      if (cancelled) return;
      if (n < LOWER_RETRY_DELAYS_MS.length) {
        retry = setTimeout(() => void attempt(n + 1), LOWER_RETRY_DELAYS_MS[n]);
        return;
      }
      lowering.current = null;
      toastRef.current.toast(AUTO_LOWER_FAILED_MESSAGE);
    };
    const timer = setInterval(() => {
      const p = room.localParticipant;
      if (auto.update(p.isSpeaking && p.isMicrophoneEnabled, Date.now())) {
        clearInterval(timer);
        void attempt(0);
      }
    }, AUTO_LOWER_TICK_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
      clearTimeout(retry);
    };
  }, [upAt, asGuest, room, send]);

  // While your hand is up, keep a fresh proof of its place, in case the room
  // loses it. A refresh never raises a hand that has come down meanwhile.
  useEffect(() => {
    if (upAt === undefined || asGuest) return;
    const timer = setInterval(() => {
      void callsApi
        .setHand(callId, true, { refresh: true })
        .then((res) => {
          if (res.data.raised && res.data.raisedAt && kept.current?.raisedAt === res.data.raisedAt) {
            kept.current = { raisedAt: res.data.raisedAt, proof: res.data.proof };
          }
        })
        .catch(() => {
          // The next one, or the proof already held, will do.
        });
    }, PROOF_REFRESH_MS);
    return () => clearInterval(timer);
  }, [upAt, asGuest, callId]);

  const value = useMemo<HandsValue>(
    () => ({
      queue,
      mine,
      canRaise: !asGuest && !!callId,
      canLowerOthers,
      pending,
      toggle: () => {
        if (pending) return;
        void change(!mine);
      },
      lower: (identity) => void change(false, identity),
      lowerAll: () => queue.forEach((h) => void change(false, h.identity)),
    }),
    [queue, mine, asGuest, callId, canLowerOthers, pending, change]
  );

  return <HandsContext.Provider value={value}>{children}</HandsContext.Provider>;
}

/** The raise and lower button among the call's controls. */
export function HandButton() {
  const hands = useHands();
  if (!hands?.canRaise) return null;
  const up = !!hands.mine;
  const label = up ? 'Lower your hand' : 'Raise your hand';
  return (
    <button
      type="button"
      className={cn('lk-button', up && '!bg-amber-300 !text-amber-950 hover:!bg-amber-200')}
      aria-label={label}
      aria-pressed={up}
      disabled={hands.pending}
      {...shortcutHint('call.hand', label)}
      onClick={hands.toggle}
      data-testid="hand-button"
    >
      <Hand className="h-5 w-5" aria-hidden="true" />
    </button>
  );
}

/** A raised hand on somebody's tile, with their place in the queue. */
export function HandBadge({ identity, small }: { identity: string; small: boolean }) {
  const hands = useHands();
  const hand = hands?.queue.find((h) => h.identity === identity);
  if (!hand) return null;
  return (
    <span
      className={cn(
        'pointer-events-none absolute left-1.5 top-1.5 z-[1] inline-flex items-center gap-1 rounded-full bg-amber-300 font-semibold tabular-nums text-amber-950 shadow-md ring-1 ring-black/10',
        small ? 'h-5 px-1.5 text-[11px]' : 'h-7 px-2 text-xs'
      )}
      role="img"
      aria-label={`Hand raised, ${ordinal(hand.position)} in line`}
      data-testid="hand-badge"
    >
      <Hand className={small ? 'h-3 w-3' : 'h-3.5 w-3.5'} aria-hidden="true" />
      {hand.position}
    </span>
  );
}

/** "2 hands raised" over the stage, opening the queue. */
export function HandsChip() {
  const hands = useHands();
  if (!hands || hands.queue.length === 0) return null;
  const { queue, canLowerOthers } = hands;
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="pointer-events-auto inline-flex h-8 items-center gap-1.5 rounded-full border border-border bg-background/95 px-3 text-sm font-medium text-foreground shadow-md backdrop-blur hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          data-testid="hands-chip"
        >
          <Hand className="h-4 w-4 text-amber-500" aria-hidden="true" />
          {handsLabel(queue.length)}
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="center" className="w-72 max-w-[calc(100vw-2rem)]" data-testid="hands-list">
        <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Raised hands, first up first</DropdownMenuLabel>
        <ol>
          {queue.map((h) => (
            <li key={h.identity} className="flex min-h-9 items-center gap-2 rounded-sm px-2 py-1 text-sm">
              <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-amber-300 text-[11px] font-semibold tabular-nums text-amber-950">
                {h.position}
              </span>
              <span className="min-w-0 flex-1 truncate">{h.name}</span>
              <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{clock(h.at)}</span>
              {canLowerOthers && (
                <DropdownMenuItem
                  className="h-7 shrink-0 px-2 text-xs font-medium"
                  aria-label={`Lower ${h.name}'s hand`}
                  onSelect={(e) => {
                    // Stay open: the organizer is often working down the list.
                    e.preventDefault();
                    hands.lower(h.identity);
                  }}
                >
                  Lower
                </DropdownMenuItem>
              )}
            </li>
          ))}
        </ol>
        {canLowerOthers && queue.length > 1 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="justify-center font-medium" onSelect={() => hands.lowerAll()}>
              Lower all
            </DropdownMenuItem>
          </>
        )}
        <p className="px-2 pb-1 pt-1.5 text-xs text-muted-foreground">A hand comes down by itself once its owner has been speaking for a few seconds.</p>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ordinal(n: number): string {
  const tens = n % 100;
  if (tens >= 11 && tens <= 13) return `${n}th`;
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`;
}

function clock(at: number): string {
  return new Date(at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}
