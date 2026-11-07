import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/shared/components/ui/button';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { FormDialog } from '@/shared/components/FormDialog';
import { scrumsApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';
import type { TaskCounts } from '@/shared/types/dashboard';
import { cn, formatDate, formatTime } from '@/shared/utils/cn';
import { DANGER_TEXT } from '../dueDate';
import { NOTES_MAX_BYTES, notesByteLength, orderPeople, personForScrum } from './logic';
import { EmptyState, ErrorState, LoadingState, ScrumPersonCard, TeamFilter, ViewFrame } from './parts';
import { useDashboard } from './useDashboard';
import { useTabParams } from './useTabParams';

/** Everybody at once, for a call. Live: it follows the work without anybody reloading. */
export function ScrumTab() {
  const { team } = useTabParams();
  const { data, error, refetch } = useDashboard(team, { poll: true });
  const [completing, setCompleting] = useState(false);

  if (!data) {
    return (
      <ViewFrame>
        {error ? (
          <ErrorState error={error} fallback="Could not load the scrum summary." onRetry={() => refetch()} />
        ) : (
          <LoadingState label="Loading the scrum summary" />
        )}
      </ViewFrame>
    );
  }

  return (
    <ViewFrame>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <TeamFilter teams={data.teams} />
        {data.people.length > 0 && (
          <Button size="sm" className="sm:ml-auto" onClick={() => setCompleting(true)}>
            Complete scrum
          </Button>
        )}
      </div>

      {data.people.length === 0 ? (
        <EmptyState
          title="Nobody to go round yet"
          hint="Add people to a team you manage, in Organization, Teams."
        />
      ) : (
        <>
          <div className="space-y-0.5">
            <Totals counts={data.totals} />
            <p className="text-xs text-muted-foreground">
              {data.lastScrumAt
                ? `Done since the last scrum, ${formatDate(data.lastScrumAt)} at ${formatTime(data.lastScrumAt)}.`
                : 'No scrum saved yet, so Done covers the last 7 days.'}
            </p>
            {error && (
              <p role="status" className={cn('text-xs', DANGER_TEXT)}>
                Could not refresh. This is the last update.
              </p>
            )}
          </div>
          <div className="space-y-2">
            {orderPeople(data.people).map((person) => (
              <ScrumPersonCard key={person.userId} row={personForScrum(person)} />
            ))}
          </div>
        </>
      )}

      <CompleteScrumDialog
        open={completing}
        onOpenChange={setCompleting}
        teamId={team}
        peopleCount={data.people.length}
      />
    </ViewFrame>
  );
}

function Totals({ counts }: { counts: TaskCounts }) {
  const parts: { value: number; label: string; warn?: boolean }[] = [
    { value: counts.blocked, label: 'blocked', warn: true },
    { value: counts.ongoing, label: 'ongoing' },
    { value: counts.pending, label: 'pending' },
    { value: counts.addedTo ?? 0, label: 'added to' },
    { value: counts.completed, label: 'done' },
    { value: counts.overdue, label: 'overdue', warn: true },
  ];
  return (
    <p className="flex flex-wrap gap-x-3 text-sm tabular-nums">
      {parts.map(({ value, label, warn }) => (
        <span key={label} className={cn(value === 0 && 'text-muted-foreground', warn && value > 0 && DANGER_TEXT)}>
          <span className="font-semibold">{value}</span> {label}
        </span>
      ))}
    </p>
  );
}

function CompleteScrumDialog({
  open,
  onOpenChange,
  teamId,
  peopleCount,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  teamId?: string;
  peopleCount: number;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [notes, setNotes] = useState('');
  const bytes = notesByteLength(notes);
  const tooLong = bytes > NOTES_MAX_BYTES;

  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setNotes('');
        onOpenChange(next);
      }}
      title="Complete scrum"
      description={`Saves where ${peopleCount === 1 ? 'this person stands' : `these ${peopleCount} people stand`} right now. A saved scrum cannot be changed.`}
      submitLabel="Complete scrum"
      disabledReason={tooLong ? 'Those notes are too long' : undefined}
      onSubmit={async () => {
        await scrumsApi.complete({ teamId, notes: notes.trim() || undefined });
        toast.success('You can read it any time under History.', 'Scrum saved');
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: ['dashboard'] }),
          queryClient.invalidateQueries({ queryKey: ['scrums'] }),
        ]);
        setNotes('');
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="scrumNotes">Notes (optional)</Label>
        <Textarea id="scrumNotes" rows={5} value={notes} onChange={(e) => setNotes(e.target.value)} autoFocus />
        <p className={cn('text-right text-xs tabular-nums text-muted-foreground', tooLong && DANGER_TEXT)}>
          {bytes} / {NOTES_MAX_BYTES} bytes
        </p>
      </div>
    </FormDialog>
  );
}
