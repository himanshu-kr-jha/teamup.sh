import { useEffect, useMemo, useState } from 'react';
import {
  ConnectionQualityIndicator,
  TrackRefContext,
  VideoTrack,
  isTrackReference,
  useIsSpeaking,
  useTracks,
} from '@livekit/components-react';
import { Track } from 'livekit-client';
import type { TrackReferenceOrPlaceholder } from '@livekit/components-react';
import { MicOff, MonitorUp, Pin, PinOff, Volume2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';

/**
 * The people in a call.
 *
 * One panel is large when there is a reason for it to be: somebody sharing
 * their screen, or somebody pinned by hand. Otherwise everybody gets equal
 * space. A shared screen wins over a pin, because it is the thing everyone
 * joined to look at, and the pin is remembered underneath so it comes back when
 * the sharing stops.
 */
export function CallStage({ callId, compact }: { callId: string; compact: boolean }) {
  const [pinnedIdentity, setPinnedIdentity] = useState<string | null>(null);

  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: false }
  );

  const screenShare = tracks.find((t) => t.source === Track.Source.ScreenShare);
  const cameras = tracks.filter((t) => t.source === Track.Source.Camera);

  const pinned = useMemo(() => {
    // Somebody presenting is what everybody is here to see, so it takes the
    // large panel whatever anybody has pinned.
    if (screenShare) return screenShare;
    if (!pinnedIdentity) return null;
    return cameras.find((t) => t.participant.identity === pinnedIdentity) ?? null;
  }, [screenShare, pinnedIdentity, cameras]);

  // A pin on somebody who has left means nothing.
  useEffect(() => {
    if (!pinnedIdentity) return;
    if (!cameras.some((t) => t.participant.identity === pinnedIdentity)) {
      setPinnedIdentity(null);
    }
  }, [cameras, pinnedIdentity]);

  const others = pinned
    ? cameras.filter(
        (t) =>
          !(
            t.participant.identity === pinned.participant.identity &&
            pinned.source === Track.Source.Camera
          )
      )
    : cameras;

  if (cameras.length === 0) {
    return (
      <div className="flex h-full items-center justify-center bg-muted/30 text-sm text-muted-foreground">
        Waiting for someone to join…
      </div>
    );
  }

  if (!pinned) {
    return (
      <div
        className={cn(
          'grid h-full gap-2 bg-muted/30 p-2',
          // Enough columns for the number of people, without going so narrow
          // that faces become thumbnails.
          cameras.length === 1 && 'grid-cols-1',
          cameras.length === 2 && 'grid-cols-1 sm:grid-cols-2',
          cameras.length >= 3 && cameras.length <= 4 && 'grid-cols-2',
          cameras.length > 4 && 'grid-cols-2 lg:grid-cols-3'
        )}
      >
        {cameras.map((track) => (
          <ParticipantPanel
            key={trackKey(track)}
            track={track}
            callId={callId}
            compact={compact}
            pinned={false}
            onTogglePin={() => setPinnedIdentity(track.participant.identity)}
          />
        ))}
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col gap-2 bg-muted/30 p-2 lg:flex-row">
      <div className="min-h-0 flex-1">
        <ParticipantPanel
          track={pinned}
          callId={callId}
          compact={compact}
          pinned
          presenting={pinned.source === Track.Source.ScreenShare}
          // A shared screen is not something to unpin; it stops when the
          // person stops sharing.
          onTogglePin={
            pinned.source === Track.Source.ScreenShare ? undefined : () => setPinnedIdentity(null)
          }
        />
      </div>

      {others.length > 0 && (
        <div
          className={cn(
            'flex gap-2 overflow-auto',
            'lg:h-full lg:w-48 lg:flex-col lg:overflow-y-auto'
          )}
        >
          {others.map((track) => (
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

  const name = participant.name || participant.identity;
  const isLocal = participant.isLocal;
  // A placeholder track has no publication at all, which is the same thing as
  // the camera being off. The guard is what lets VideoTrack be given a
  // reference it can actually render.
  const hasVideo = isTrackReference(track) && (presenting || !track.publication.isMuted);
  const microphoneOff = !participant.isMicrophoneEnabled;

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
          'group relative h-full w-full overflow-hidden rounded-lg bg-black/80',
          // Whoever is talking is outlined, so a room of faces still tells you
          // where to look.
          speaking ? 'ring-2 ring-green-500' : 'ring-1 ring-border/50'
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
            <span
              className={cn(
                'flex items-center justify-center rounded-full bg-primary/20 font-semibold text-primary',
                compact ? 'h-10 w-10 text-sm' : 'h-20 w-20 text-2xl'
              )}
            >
              {initialsOf(name)}
            </span>
          </div>
        )}

        {/* The name, and whether they can be heard. */}
        <div className="absolute inset-x-0 bottom-0 flex items-center gap-1.5 bg-gradient-to-t from-black/70 to-transparent px-2 py-1.5">
          {microphoneOff ? (
            <MicOff className="h-3.5 w-3.5 shrink-0 text-red-400" aria-label="Microphone off" />
          ) : (
            <Volume2
              className={cn('h-3.5 w-3.5 shrink-0', speaking ? 'text-green-400' : 'text-white/60')}
              aria-hidden="true"
            />
          )}
          <span className="truncate text-xs font-medium text-white">
            {presenting ? `${name} is presenting` : name}
            {isLocal && ' (you)'}
          </span>
          {presenting && <MonitorUp className="h-3.5 w-3.5 shrink-0 text-white/80" aria-hidden="true" />}
          <span className="ml-auto shrink-0">
            <ConnectionQualityIndicator participant={participant} />
          </span>
        </div>

        {/* Controls, out of the way until wanted. */}
        {!compact && (
          <div className="absolute right-1 top-1 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
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
            {!isLocal && !microphoneOff && (
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

/** Up to two initials, which is what fits legibly in a circle. */
function initialsOf(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}
