import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { CalendarClock, History, Loader2, Lock, Pencil, Timer, Trash2, Video } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { calendarApi, meetingsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { useAuth } from '@/features/auth/AuthProvider';
import { CallLogDialog } from '@/features/calls/CallLogDialog';
import { useJoinStanding } from '@/features/calls/standing/StandingMeetings';
import { JOIN_EARLY_MINUTES, extendedText, itemLabel, joinTarget, kindLabel, meetingStillOn, type CalendarItem } from './layout';

const SOURCE_LABEL: Record<string, string> = {
  block: 'Your time',
  meeting: 'Scheduled meeting',
  call: 'Call',
  standing: 'Standing meeting',
};

/** One thing on a calendar, and whatever may be done with it. */
export function EventDialog({
  item,
  own,
  now,
  people,
  onClose,
  onEditBlock,
  onEditMeeting,
}: {
  item: CalendarItem;
  own: boolean;
  now: Date;
  people: { id: string; name: string }[];
  onClose: () => void;
  onEditBlock: (item: CalendarItem) => void;
  onEditMeeting: (item: CalendarItem) => void;
}) {
  const { user } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const toast = useToast();
  const { joinStanding, joining } = useJoinStanding();
  const [confirming, setConfirming] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [showingLog, setShowingLog] = useState(false);

  // The organizer is named on their own line.
  const invited = (item.inviteeIds ?? []).filter((id) => id !== item.organizerId);
  const nameOf = (id: string) => (id === user?.id ? 'You' : people.find((p) => p.id === id)?.name ?? 'Somebody');
  const target = joinTarget(item, now);
  const ownBlock = own && item.source === 'block' && !!item.id;
  const isAdmin = user?.roles?.some((r) => r === 'owner' || r === 'admin') ?? false;
  // Both are checked again by the server, which is what really decides.
  const canChangeMeeting =
    item.source === 'meeting' && item.status === 'scheduled' && !!item.callId && (item.organizerId === user?.id || isAdmin);
  // Nobody is offered a log that can only refuse them, as a standing meeting's does.
  const hasLog =
    !!item.callId && item.canReadLog !== false && (item.source === 'call' || item.status === 'active' || item.status === 'ended');
  const stillOn = item.source === 'meeting' && meetingStillOn({ status: item.status, scheduledEnd: item.endsAt }, now);
  const startsLater =
    item.source === 'meeting' &&
    (item.status === 'scheduled' || stillOn) &&
    new Date(item.startsAt).getTime() - JOIN_EARLY_MINUTES * 60_000 > now.getTime();
  const noDetail = !item.source;

  const sameDay = new Date(item.startsAt).toDateString() === new Date(item.endsAt).toDateString();

  const remove = async () => {
    setRemoving(true);
    try {
      if (ownBlock) await calendarApi.deleteBlock(item.id!);
      else await meetingsApi.cancel(item.callId!);
      await queryClient.invalidateQueries({ queryKey: ['calendar'] });
      await queryClient.invalidateQueries({ queryKey: ['calls'] });
      toast.success(ownBlock ? 'Removed from your calendar.' : 'Meeting cancelled. Everybody invited has been told.');
      onClose();
    } catch (err) {
      toast.error(apiErrorMessage(err, ownBlock ? 'Could not remove that.' : 'Could not cancel the meeting.'));
      setRemoving(false);
      setConfirming(false);
    }
  };

  return (
    <>
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="break-anywhere pr-6">{itemLabel(item)}</DialogTitle>
            <DialogDescription className="tabular-nums">
              {formatDate(item.startsAt)}, {formatTime(item.startsAt)} to{' '}
              {sameDay ? formatTime(item.endsAt) : `${formatDate(item.endsAt)}, ${formatTime(item.endsAt)}`}
            </DialogDescription>
          </DialogHeader>

          <div className="min-w-0 space-y-2 text-sm">
            <p className="flex items-center gap-2 text-muted-foreground">
              <CalendarClock className="h-4 w-4 shrink-0" aria-hidden="true" />
              {item.source ? SOURCE_LABEL[item.source] ?? kindLabel(item.kind) : kindLabel(item.kind)}
              {item.status === 'ended' && !stillOn && ', finished'}
              {item.status === 'active' && ', happening now'}
            </p>
            {extendedText(item) && (
              <p className="flex items-center gap-2 font-medium text-foreground">
                <Timer className="h-4 w-4 shrink-0" aria-hidden="true" />
                {extendedText(item)}
              </p>
            )}
            {noDetail && (
              <p className="flex items-center gap-2 text-muted-foreground">
                <Lock className="h-4 w-4 shrink-0" aria-hidden="true" />
                Only the times are shared with you.
              </p>
            )}
            {item.organizerId && (
              <p className="break-anywhere">
                <span className="text-muted-foreground">Organizer: </span>
                {nameOf(item.organizerId)}
              </p>
            )}
            {invited.length > 0 && (
              <p className="break-anywhere">
                <span className="text-muted-foreground">Invited: </span>
                {invited.map(nameOf).join(', ')}
              </p>
            )}
            {startsLater && (
              <p className="text-muted-foreground">You can join from {JOIN_EARLY_MINUTES} minutes before it starts.</p>
            )}
          </div>

          {confirming ? (
            <DialogFooter className="gap-2">
              <p className="mr-auto self-center text-sm">
                {ownBlock ? 'Remove this from your calendar?' : 'Cancel this meeting for everybody?'}
              </p>
              <Button variant="outline" disabled={removing} onClick={() => setConfirming(false)}>
                Keep it
              </Button>
              <Button variant="destructive" disabled={removing} onClick={remove}>
                {removing && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
                {ownBlock ? 'Remove' : 'Cancel meeting'}
              </Button>
            </DialogFooter>
          ) : (
            (target || ownBlock || canChangeMeeting || hasLog) && (
              <DialogFooter className="flex-row flex-wrap justify-end gap-2 sm:space-x-0">
                {(ownBlock || canChangeMeeting) && (
                  <Button variant="outline" onClick={() => setConfirming(true)}>
                    <Trash2 className="mr-1 h-4 w-4" aria-hidden="true" />
                    {ownBlock ? 'Remove' : 'Cancel meeting'}
                  </Button>
                )}
                {(ownBlock || canChangeMeeting) && (
                  <Button variant="outline" onClick={() => (ownBlock ? onEditBlock(item) : onEditMeeting(item))}>
                    <Pencil className="mr-1 h-4 w-4" aria-hidden="true" />
                    Edit
                  </Button>
                )}
                {hasLog && (
                  <Button variant="outline" onClick={() => setShowingLog(true)}>
                    <History className="mr-1 h-4 w-4" aria-hidden="true" />
                    Log
                  </Button>
                )}
                {target && (
                  <Button
                    disabled={joining !== null}
                    onClick={() => {
                      if (target.type === 'call') {
                        onClose();
                        navigate(`/calls/${target.callId}`, { state: { join: true } });
                      } else {
                        void joinStanding(target.standingMeetingId).then(onClose);
                      }
                    }}
                  >
                    {joining !== null ? (
                      <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" />
                    ) : (
                      <Video className="mr-1 h-4 w-4" aria-hidden="true" />
                    )}
                    Join
                  </Button>
                )}
              </DialogFooter>
            )
          )}
        </DialogContent>
      </Dialog>
      {item.callId && (
        <CallLogDialog callId={item.callId} open={showingLog} onOpenChange={setShowingLog} live={item.status === 'active'} />
      )}
    </>
  );
}
