import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CameraOff, MapPin } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { fieldWorkApi, standingApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn, formatTime } from '@/shared/utils/cn';
import type { AttendanceDay, AttendancePerson, AttendanceStatus } from './standing';

export const STATUS_LABEL: Record<AttendanceStatus, string> = {
  present: 'Present',
  absent: 'Absent',
  unknown: 'Unknown',
  on_leave: 'On leave',
  holiday: 'Holiday',
  field_work: 'Field work',
  review: 'In review',
};

/** A map at the place, on OpenStreetMap, which needs no key and no account. */
export function mapLink(latitude: number, longitude: number): string {
  return `https://www.openstreetmap.org/?mlat=${latitude.toFixed(5)}&mlon=${longitude.toFixed(5)}#map=16/${latitude.toFixed(5)}/${longitude.toFixed(5)}`;
}

function shortDay(at: string): string {
  return new Date(at).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}

/** What field work counted, with its photo a click away. */
export function FieldDetail({ person }: { person: AttendancePerson }) {
  const [open, setOpen] = useState(false);
  const field = person.field;
  if (!field) return null;
  return (
    <span className="mt-0.5 block text-xs text-muted-foreground">
      <button
        type="button"
        className="underline underline-offset-2 hover:text-foreground"
        onClick={() => setOpen(true)}
        aria-label={`Show ${person.name}'s field work photo`}
      >
        Photo at {formatTime(field.takenAt)}
      </button>
      {', '}
      <a
        href={mapLink(field.latitude, field.longitude)}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-0.5 underline underline-offset-2 hover:text-foreground"
      >
        <MapPin className="h-3 w-3" aria-hidden="true" />
        on the map
      </a>
      {field.note ? `: ${field.note}` : ''}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{person.name}, field work</DialogTitle>
            <DialogDescription>
              Taken in the app at {formatTime(field.takenAt)} on {shortDay(field.takenAt)}, within about{' '}
              {Math.round(field.accuracyM)} m of the place on the map.
            </DialogDescription>
          </DialogHeader>
          {open && (
            <img
              src={fieldWorkApi.photoUrl(field.id)}
              alt={`Field work photo from ${person.name}`}
              className="max-h-[60vh] w-full rounded-md bg-muted object-contain"
            />
          )}
          {field.note && <p className="text-sm">{field.note}</p>}
          <a
            href={mapLink(field.latitude, field.longitude)}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-sm underline underline-offset-2"
          >
            <MapPin className="h-4 w-4" aria-hidden="true" />
            Open the place on a map ({field.latitude.toFixed(4)}, {field.longitude.toFixed(4)})
          </a>
        </DialogContent>
      </Dialog>
    </span>
  );
}

/** On a day that required cameras, somebody who never turned theirs on. */
export function CameraDetail({ person }: { person: AttendancePerson }) {
  if (person.cameraOn !== false) return null;
  return (
    <span className="mt-0.5 flex items-center gap-1 text-xs text-amber-700 dark:text-amber-400">
      <CameraOff className="h-3 w-3" aria-hidden="true" />
      Camera never turned on
    </span>
  );
}

/** A reviewer's decision, with who made it and what the day said before. */
export function ReviewDetail({ person }: { person: AttendancePerson }) {
  const review = person.review;
  if (!review) return null;
  const who = review.decidedBy ? review.decidedBy.name : 'somebody who has since left';
  return (
    <span className="mt-0.5 block text-xs text-muted-foreground">
      Decided by {who} on {shortDay(review.decidedAt)}, was {STATUS_LABEL[review.was].toLowerCase()}
      {review.note ? `: ${review.note}` : ''}
    </span>
  );
}

/** Whether a reviewer is offered a decision for somebody's day. */
export function reviewable(person: AttendancePerson): boolean {
  return !!person.review || ['review', 'field_work', 'absent', 'unknown'].includes(person.status);
}

export function ReviewButton({ meetingId, day, person }: { meetingId: string; day: AttendanceDay; person: AttendancePerson }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant={person.status === 'review' ? 'default' : 'ghost'}
        size="sm"
        className={cn('h-7 px-2 text-xs', person.status !== 'review' && 'text-muted-foreground hover:text-foreground')}
        onClick={() => setOpen(true)}
        aria-label={`Decide ${person.name} on ${day.date}`}
      >
        {person.review ? 'Change' : 'Decide'}
      </Button>
      {open && <ReviewDialog meetingId={meetingId} day={day} person={person} onClose={() => setOpen(false)} />}
    </>
  );
}

function ReviewDialog({
  meetingId,
  day,
  person,
  onClose,
}: {
  meetingId: string;
  day: AttendanceDay;
  person: AttendancePerson;
  onClose: () => void;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [decision, setDecision] = useState<'present' | 'absent'>(person.review?.decision ?? 'present');
  const [note, setNote] = useState(person.review?.note ?? '');
  const save = useMutation({
    mutationFn: (d: 'present' | 'absent' | '') =>
      standingApi.review(meetingId, { date: day.date, userId: person.userId, decision: d, note: d ? note : '' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['attendance', meetingId] });
      onClose();
    },
    onError: (err) => toast.error(apiErrorMessage(err, 'Could not save the decision.')),
  });
  const was = person.review ? person.review.was : person.status;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Decide {person.name}</DialogTitle>
          <DialogDescription>
            {new Date(`${day.date}T00:00:00`).toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric' })}. The
            day says {STATUS_LABEL[was].toLowerCase()}
            {person.cameraOn === false ? ': they joined but never turned their camera on' : ''}
            {was === 'field_work' ? ': they sent a photo from the field' : ''}. Your decision is kept with your name.
          </DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-1 rounded-md bg-muted p-1" role="radiogroup" aria-label="Decision">
          {(['present', 'absent'] as const).map((d) => (
            <button
              key={d}
              type="button"
              role="radio"
              aria-checked={decision === d}
              onClick={() => setDecision(d)}
              className={cn(
                'rounded px-3 py-1.5 text-sm font-medium',
                decision === d ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'
              )}
            >
              {STATUS_LABEL[d]}
            </button>
          ))}
        </div>
        <div className="space-y-1">
          <Label htmlFor="reviewNote">Note (optional)</Label>
          <Textarea
            id="reviewNote"
            rows={3}
            maxLength={1000}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="Why, for whoever reads this later"
          />
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          {person.review && (
            <Button variant="ghost" className="mr-auto" disabled={save.isPending} onClick={() => save.mutate('')}>
              Take back the decision
            </Button>
          )}
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={save.isPending} onClick={() => save.mutate(decision)}>
            Save decision
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
