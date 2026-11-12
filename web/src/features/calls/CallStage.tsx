import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  ConnectionQualityIndicator,
  TrackRefContext,
  VideoTrack,
  isTrackReference,
  useIsSpeaking,
  useSpeakingParticipants,
  useTracks,
} from '@livekit/components-react';
import { Track } from 'livekit-client';
import type { TrackReferenceOrPlaceholder } from '@livekit/components-react';
import { ChevronLeft, ChevronRight, MicOff, MonitorUp, NotebookPen, Pin, PinOff, UserX, Volume2 } from 'lucide-react';
import { isNotetaker } from './notetaker';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import {
  MAX_TILES_COMPACT,
  MAX_TILES_PER_PAGE,
  TILE_GAP,
  bestGrid,
  clampPage,
  pageCount,
  pageOf,
  promoteSpeakers,
  reconcileOrder,
  stageShare,
} from './tileLayout';

/**
 * The people in a call.
 *
 * One panel is large when there is a reason for it to be: somebody sharing their
 * screen, or somebody pinned by hand. Otherwise everybody gets the largest equal
 * tile the space allows. A shared screen wins over a pin, because it is the thing
 * everyone joined to look at, and the pin is remembered underneath so it comes back
 * when the sharing stops. Several people can share at once: one is on stage and the
 * others wait in the strip, a click away.
 */
/** The guest identity prefix (internal/calls). */
export const GUEST_PREFIX = 'guest-';

export function isGuest(p: { identity: string }): boolean {
  return p.identity.startsWith(GUEST_PREFIX);
}

/** Who is looking at the stage: a guest has no account to mute or remove with. */
const ViewerContext = createContext<{ asGuest: boolean; canRemoveGuests: boolean }>({ asGuest: false, canRemoveGuests: false });

export function CallStage({ callId, compact, asGuest = false }: { callId: string; compact: boolean; asGuest?: boolean }) {
  const { data: guestLink } = useQuery({
    queryKey: ['guest-link', callId],
    queryFn: async () => (await callsApi.guestLink(callId)).data,
    enabled: !asGuest,
  });
  const viewer = useMemo(() => ({ asGuest, canRemoveGuests: !asGuest && !!guestLink?.canManage }), [asGuest, guestLink?.canManage]);
  return (
    <ViewerContext.Provider value={viewer}>
      <Stage callId={callId} compact={compact} />
    </ViewerContext.Provider>
  );
}

function Stage({ callId, compact }: { callId: string; compact: boolean }) {
  const [pinnedIdentity, setPinnedIdentity] = useState<string | null>(null);
  const [chosenShare, setChosenShare] = useState<string | null>(null);

  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: false }
  );

  const shares = tracks.filter((t) => t.source === Track.Source.ScreenShare);
  const cameras = tracks.filter((t) => t.source === Track.Source.Camera);

  const shareOnStageKey = stageShare(shares.map(trackKey), chosenShare);
  const shareOnStage = shares.find((t) => trackKey(t) === shareOnStageKey) ?? null;
  const pinnedCamera = pinnedIdentity
    ? cameras.find((t) => t.participant.identity === pinnedIdentity) ?? null
    : null;
  const stage = shareOnStage ?? pinnedCamera;

  // A pin on somebody who has left means nothing.
  useEffect(() => {
    if (!pinnedIdentity) return;
    if (!cameras.some((t) => t.participant.identity === pinnedIdentity)) {
      setPinnedIdentity(null);
    }
  }, [cameras, pinnedIdentity]);

  if (cameras.length === 0) {
    return (
      <div className="flex h-full items-center justify-center bg-muted/30 text-sm text-muted-foreground">
        Waiting for someone to join…
      </div>
    );
  }

  if (!stage) {
    return (
      <TileGrid
        cameras={cameras}
        callId={callId}
        compact={compact}
        onPin={(identity) => setPinnedIdentity(identity)}
      />
    );
  }

  const stageIsShare = stage.source === Track.Source.ScreenShare;

  // The corner window has room for the stage and nothing else.
  if (compact) {
    return (
      <div className="h-full bg-muted/30 p-1">
        <ParticipantPanel track={stage} callId={callId} compact pinned presenting={stageIsShare} />
      </div>
    );
  }

  const otherShares = shares.filter((t) => t !== stage);
  const otherCameras = cameras.filter((t) => stageIsShare || t.participant.identity !== stage.participant.identity);

  return (
    <div className="flex h-full flex-col gap-2 bg-muted/30 p-2 lg:flex-row">
      <div className="min-h-0 flex-1">
        <ParticipantPanel
          track={stage}
          callId={callId}
          compact={false}
          pinned
          presenting={stageIsShare}
          // A shared screen is not something to unpin; it stops when the person
          // stops sharing.
          onTogglePin={stageIsShare ? undefined : () => setPinnedIdentity(null)}
        />
      </div>

      {(otherShares.length > 0 || otherCameras.length > 0) && (
        <div className="flex shrink-0 gap-2 overflow-x-auto lg:h-full lg:w-48 lg:flex-col lg:overflow-y-auto lg:overflow-x-visible">
          {otherShares.map((track) => (
            <div key={trackKey(track)} className="relative h-24 w-40 shrink-0 lg:h-28 lg:w-full">
              <ParticipantPanel track={track} callId={callId} compact pinned={false} presenting />
              <button
                type="button"
                className="absolute inset-0 rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                aria-label={`Show ${nameOf(track)}'s screen on stage`}
                title="Show on stage"
                onClick={() => setChosenShare(trackKey(track))}
              />
            </div>
          ))}
          {otherCameras.map((track) => (
            <div key={trackKey(track)} className="h-24 w-40 shrink-0 lg:h-28 lg:w-full">
              <ParticipantPanel
                track={track}
                callId={callId}
                compact
                pinned={false}
                onTogglePin={() => setPinnedIdentity(track.participant.identity)}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** How much room an element really has, kept up to date as it changes. */
function useMeasured() {
  const ref = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;
    const measure = () => setSize({ width: element.clientWidth, height: element.clientHeight });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return { ref, ...size };
}

function TileGrid({
  cameras,
  callId,
  compact,
  onPin,
}: {
  cameras: TrackReferenceOrPlaceholder[];
  callId: string;
  compact: boolean;
  onPin: (identity: string) => void;
}) {
  const { ref, width, height } = useMeasured();
  const pageSize = compact ? MAX_TILES_COMPACT : MAX_TILES_PER_PAGE;

  const identities = cameras.map((t) => t.participant.identity);
  const [storedOrder, setStoredOrder] = useState<string[]>(identities);
  const order = reconcileOrder(storedOrder, identities);

  const [requestedPage, setPage] = useState(0);
  const page = clampPage(requestedPage, order.length, pageSize);
  const pages = pageCount(order.length, pageSize);

  const speakers = useSpeakingParticipants().map((p) => p.identity);
  const speakersKey = speakers.join('|');
  const orderKey = order.join('|');
  useEffect(() => {
    const next = promoteSpeakers(order, page, pageSize, speakers);
    if (next !== order || order !== storedOrder) setStoredOrder(next);
    // The joined keys stand for the arrays, which are new on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [speakersKey, orderKey, page, pageSize]);

  const byIdentity = useMemo(() => new Map(cameras.map((t) => [t.participant.identity, t])), [cameras]);
  const visible = pageOf(order, page, pageSize)
    .map((identity) => byIdentity.get(identity))
    .filter((t): t is TrackReferenceOrPlaceholder => !!t);

  const grid = bestGrid(visible.length, width, height, TILE_GAP);
  const rowWidth = grid.columns * grid.tileWidth + (grid.columns - 1) * TILE_GAP;
  // A tile too small for a big circle and for hover controls is drawn as a small one.
  const small = compact || grid.tileHeight < 130;

  return (
    <div className={cn('flex h-full flex-col bg-muted/30', compact ? 'p-1' : 'p-2')}>
      <div ref={ref} className="flex min-h-0 flex-1 items-center justify-center overflow-hidden">
        {grid.tileWidth > 0 && (
          <div className="flex flex-wrap content-center justify-center" style={{ width: rowWidth, gap: TILE_GAP }}>
            {visible.map((track) => (
              <div key={trackKey(track)} style={{ width: grid.tileWidth, height: grid.tileHeight }}>
                <ParticipantPanel
                  track={track}
                  callId={callId}
                  compact={small}
                  pinned={false}
                  onTogglePin={compact || order.length < 2 ? undefined : () => onPin(track.participant.identity)}
                />
              </div>
            ))}
          </div>
        )}
      </div>
      {pages > 1 && !compact && (
        <nav className="mt-2 flex shrink-0 items-center justify-center gap-2 text-sm" aria-label="Pages of people">
          <Button
            variant="outline"
            size="icon"
            className="h-8 w-8"
            disabled={page === 0}
            aria-label="Previous page of people"
            onClick={() => setPage(page - 1)}
          >
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <span className="tabular-nums text-muted-foreground">
            Page {page + 1} of {pages}
          </span>
          <Button
            variant="outline"
            size="icon"
            className="h-8 w-8"
            disabled={page >= pages - 1}
            aria-label="Next page of people"
            onClick={() => setPage(page + 1)}
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
        </nav>
      )}
    </div>
  );
}

// A small tile's picture and initials, in proportion to the tile (cqmin is a
// share of its shorter side) between the old fixed size and the full one.
const SMALL_PICTURE = 'h-[clamp(2rem,38cqmin,5rem)] w-[clamp(2rem,38cqmin,5rem)]';
const SMALL_INITIALS = 'text-[clamp(0.75rem,13cqmin,1.5rem)]';

/** One person, or one shared screen. */
function ParticipantPanel({
  track,
  callId,
  compact,
  pinned,
  presenting,
  onTogglePin,
}: {
  track: TrackReferenceOrPlaceholder;
  callId: string;
  compact: boolean;
  pinned: boolean;
  presenting?: boolean;
  onTogglePin?: () => void;
}) {
  const toast = useToast();
  const participant = track.participant;
  const speaking = useIsSpeaking(participant);
  const [muting, setMuting] = useState(false);
  const [removing, setRemoving] = useState(false);
  const viewer = useContext(ViewerContext);
  const guest = isGuest(participant);

  const name = nameOf(track);
  const isLocal = participant.isLocal;
  // A placeholder track has no publication at all, which is the same thing as
  // the camera being off. The guard is what lets VideoTrack be given a
  // reference it can actually render.
  const hasVideo = isTrackReference(track) && (presenting || !track.publication.isMuted);
  const microphoneOff = !participant.isMicrophoneEnabled;
  const notes = isNotetaker(participant);

  const caption = notes
    ? 'Notes, taking the minutes'
    : presenting
    ? isLocal
      ? 'You are presenting'
      : `${name} is presenting`
    : `${name}${guest ? ' (guest)' : ''}${isLocal ? ' (you)' : ''}`;

  const remove = async () => {
    setRemoving(true);
    try {
      await callsApi.removeGuest(callId, participant.identity.slice(GUEST_PREFIX.length));
      toast.success(`${name} was removed from the meeting.`);
    } catch (err) {
      toast.error(apiErrorMessage(err, `Could not remove ${name}.`));
      setRemoving(false);
    }
  };

  const mute = async () => {
    setMuting(true);
    try {
      await callsApi.muteParticipant(callId, participant.identity);
      toast.success(`${name} was muted.`);
    } catch (err) {
      toast.error(apiErrorMessage(err, `Could not mute ${name}.`));
    } finally {
      setMuting(false);
    }
  };

  return (
    <TrackRefContext.Provider value={track}>
      <div
        className={cn(
          // A size container, so a small tile's picture grows with the tile:
          // the minimised call window can be made bigger.
          'group relative h-full w-full overflow-hidden rounded-lg bg-black/80 [container-type:size]',
          // Whoever is talking is outlined, so a room of faces still tells you
          // where to look.
          speaking && !presenting ? 'ring-2 ring-green-500' : 'ring-1 ring-border/50'
        )}
      >
        {isTrackReference(track) && hasVideo ? (
          <VideoTrack
            trackRef={track}
            className={cn('h-full w-full', presenting ? 'object-contain' : 'object-cover')}
          />
        ) : (
          // A camera that is off should show the person, not a black rectangle.
          <div className="flex h-full w-full items-center justify-center">
            {notes ? (
              <span
                className={cn(
                  'flex items-center justify-center rounded-full bg-primary/20 font-semibold text-primary',
                  compact ? cn(SMALL_PICTURE, SMALL_INITIALS) : 'h-20 w-20 text-2xl'
                )}
              >
                <NotebookPen className={compact ? 'h-1/2 w-1/2' : 'h-9 w-9'} aria-label="Notes" />
              </span>
            ) : (
              <PersonAvatar
                // A member's identity is their id; a guest has none to look up.
                userId={guest ? null : participant.identity}
                name={name}
                className={compact ? SMALL_PICTURE : 'h-20 w-20'}
                fallbackClassName={cn('bg-primary/20 font-semibold text-primary', compact ? SMALL_INITIALS : 'text-2xl')}
              />
            )}
          </div>
        )}

        {/* The name, and whether they can be heard. */}
        <div className="absolute inset-x-0 bottom-0 flex items-center gap-1.5 bg-gradient-to-t from-black/70 to-transparent px-2 py-1.5 text-white">
          {!presenting &&
            !notes &&
            (microphoneOff ? (
              <MicOff className="h-3.5 w-3.5 shrink-0 text-red-400" aria-label="Microphone off" />
            ) : (
              <Volume2
                className={cn('h-3.5 w-3.5 shrink-0', speaking ? 'text-green-400' : 'text-white/60')}
                aria-hidden="true"
              />
            ))}
          {presenting && <MonitorUp className="h-3.5 w-3.5 shrink-0 text-white/80" aria-hidden="true" />}
          <span className="truncate text-xs font-medium text-white">{caption}</span>
          <span className="ml-auto shrink-0">
            <ConnectionQualityIndicator participant={participant} />
          </span>
        </div>

        {/* Controls, out of the way until wanted. A touch screen has no hover, so
            there they simply stay. */}
        {!compact && !notes && (
          <div className="absolute right-1 top-1 flex gap-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100">
            {onTogglePin && (
              <Button
                size="icon"
                variant="secondary"
                className="h-7 w-7"
                onClick={onTogglePin}
                aria-label={pinned ? `Unpin ${name}` : `Pin ${name}`}
                title={pinned ? 'Unpin' : 'Pin'}
              >
                {pinned ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}
              </Button>
            )}
            {/* Muting is one way: anybody can quieten a forgotten microphone,
                but only its owner can turn it back on. */}
            {guest && !isLocal && viewer.canRemoveGuests && (
              <Button
                size="icon"
                variant="secondary"
                className="h-7 w-7"
                disabled={removing}
                onClick={remove}
                aria-label={`Remove ${name} from the meeting`}
                title={`Remove ${name}`}
              >
                <UserX className="h-3.5 w-3.5" />
              </Button>
            )}
            {!isLocal && !microphoneOff && !viewer.asGuest && (
              <Button
                size="icon"
                variant="secondary"
                className="h-7 w-7"
                disabled={muting}
                onClick={mute}
                aria-label={`Mute ${name}`}
                title={`Mute ${name}`}
              >
                <MicOff className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        )}
      </div>
    </TrackRefContext.Provider>
  );
}

function trackKey(track: TrackReferenceOrPlaceholder) {
  return `${track.participant.identity}-${track.source}-${track.publication?.trackSid ?? 'none'}`;
}

function nameOf(track: TrackReferenceOrPlaceholder) {
  return track.participant.name || track.participant.identity;
}
