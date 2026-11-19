import { useEffect, useRef } from 'react';
import { useRoomContext } from '@livekit/components-react';
import { ConnectionState, RoomEvent, type RemoteParticipant } from 'livekit-client';
import { ArrowRightLeft, Video, VolumeX } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import type { OtherDevice } from '@/shared/api/client';
import { MOVED_TOPIC, movedPacket, readMoved, type DeviceKind } from './devices';

// Companion mode's parts that are seen: the question asked when you join a call
// you are already in from another device, the note on a device that joined as a
// second camera, and the word passed between your devices when one takes over.

/** How joining from this device was answered. */
export type CompanionAnswer = 'camera' | 'everything';

function deviceWord(kind: DeviceKind | undefined): string {
  return kind ?? 'device';
}

/**
 * Asked when you join a call you are already in from another device: is this one
 * only a second camera, or does the call move here?
 */
export function CompanionChoice({
  open,
  here,
  others,
  onAnswer,
  onCancel,
}: {
  open: boolean;
  /** This device. */
  here: DeviceKind;
  /** Where else you are in the call. */
  others: OtherDevice[];
  onAnswer: (answer: CompanionAnswer) => void;
  onCancel: () => void;
}) {
  const there = others.length === 1 ? `your ${deviceWord(others[0].kind)}` : 'your other devices';
  const thisOne = `this ${deviceWord(here)}`;
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onCancel()}>
      <DialogContent className="max-w-md" data-testid="companion-choice">
        <DialogHeader>
          <DialogTitle>You are already in this call</DialogTitle>
          <DialogDescription>
            You are in it on {there}. How should {thisOne} join?
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <ChoiceButton
            id="companion-camera"
            icon={<Video className="h-5 w-5" aria-hidden="true" />}
            title="Camera only"
            detail={`A second angle. ${capitalize(thisOne)} joins with its microphone and sound off, so there is no echo.`}
            onClick={() => onAnswer('camera')}
          />
          <ChoiceButton
            id="companion-everything"
            icon={<ArrowRightLeft className="h-5 w-5" aria-hidden="true" />}
            title={`Use ${thisOne} for everything`}
            detail={`${capitalize(there)} ${others.length === 1 ? 'leaves' : 'leave'} the call, and ${thisOne} takes over.`}
            onClick={() => onAnswer('everything')}
          />
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChoiceButton({
  id,
  icon,
  title,
  detail,
  onClick,
}: {
  id: string;
  icon: React.ReactNode;
  title: string;
  detail: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-labelledby={`${id}-title`}
      aria-describedby={`${id}-detail`}
      onClick={onClick}
      className="flex w-full items-start gap-3 rounded-lg border border-border bg-background p-3 text-left transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <span className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">{icon}</span>
      <span className="min-w-0">
        <span id={`${id}-title`} className="block text-sm font-medium text-foreground">
          {title}
        </span>
        <span id={`${id}-detail`} className="mt-0.5 block text-sm leading-snug text-muted-foreground">
          {detail}
        </span>
      </span>
    </button>
  );
}

function capitalize(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/**
 * Said on a device that joined as a second camera, for as long as its sound is
 * off: its microphone is off too, and can be turned on with the usual button.
 */
export function CompanionNote({ kind, onTurnOnSound }: { kind: DeviceKind; onTurnOnSound: () => void }) {
  return (
    <div
      role="status"
      data-testid="companion-note"
      className="flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 border-b border-border bg-primary/10 px-3 py-1.5 text-center text-xs"
    >
      <span className="inline-flex items-center gap-2">
        <VolumeX className="h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />
        <span>Camera only: this {kind}'s microphone and sound are off, so there is no echo.</span>
      </span>
      <Button size="sm" variant="outline" className="h-7 bg-background" onClick={onTurnOnSound}>
        Turn on sound
      </Button>
    </div>
  );
}

/** How often, and how many times, the word to step aside is said while a device has not gone. */
const MOVE_RETRY_MS = 2_000;
const MOVE_TRIES = 6;

/**
 * Passes the call between your devices. A device that took the call over
 * (takeOverFrom) tells the others to step aside once it is in, and again while
 * any of them is still there; a device told so by another of yours leaves, and
 * says where the call went (onMoved).
 */
export function DeviceMoves({
  takeOverFrom,
  here,
  onMoved,
}: {
  takeOverFrom?: string[];
  here: DeviceKind;
  onMoved: (to: DeviceKind | undefined) => void;
}) {
  const room = useRoomContext();
  const onMovedRef = useRef(onMoved);
  onMovedRef.current = onMoved;

  useEffect(() => {
    const onData = (payload: Uint8Array, from?: RemoteParticipant, _kind?: unknown, topic?: string) => {
      const moved = readMoved(payload, from, topic, room.localParticipant.identity);
      if (moved) onMovedRef.current(moved.to);
    };
    room.on(RoomEvent.DataReceived, onData);
    return () => {
      room.off(RoomEvent.DataReceived, onData);
    };
  }, [room]);

  const targets = takeOverFrom?.join('|') ?? '';
  useEffect(() => {
    if (!targets) return;
    const identities = targets.split('|');
    let tries = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stillThere = () => identities.filter((id) => room.remoteParticipants.has(id));
    const tell = () => {
      if (room.state !== ConnectionState.Connected) return;
      const waiting = stillThere();
      if (waiting.length === 0 || tries >= MOVE_TRIES) return;
      tries += 1;
      void room.localParticipant
        .publishData(movedPacket(here), { reliable: true, topic: MOVED_TOPIC, destinationIdentities: waiting })
        .catch(() => undefined);
      timer = setTimeout(tell, MOVE_RETRY_MS);
    };
    if (room.state === ConnectionState.Connected) tell();
    room.on(RoomEvent.Connected, tell);
    return () => {
      clearTimeout(timer);
      room.off(RoomEvent.Connected, tell);
    };
  }, [room, targets, here]);

  return null;
}
