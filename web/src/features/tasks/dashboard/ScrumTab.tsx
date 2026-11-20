import { useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/shared/components/ui/button';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { FormDialog } from '@/shared/components/FormDialog';
import { scrumsApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';
import type { ScrumOrder, TaskCounts } from '@/shared/types/dashboard';
import { cn, formatDate, formatTime } from '@/shared/utils/cn';
import { DANGER_TEXT } from '../dueDate';
import { NOTES_MAX_BYTES, notesByteLength } from './logic';
import { EmptyState, ErrorState, LoadingState, TeamFilter, ViewFrame } from './parts';
import { ScrumTable } from './ScrumTable';
import { liveScrumRows, orderOf } from './scrumTable';
import { useDashboard } from './useDashboard';
import { useScrumOrder } from './useScrumOrder';
import { useTabParams } from './useTabParams';

/**
 * Everybody at once, for a call, as a table to go down. Live: it follows the work
 * without anybody reloading. The order is the viewer's own, kept on the server.
 */
export function ScrumTab() {
  const { team } = useTabParams();
  const { data, error, refetch } = useDashboard(team, { poll: true });
  // Somebody who answers only for themselves is about to be sent to Overview, and
  // has no order to ask for.
  const {
    order,
    isPending: orderPending,
    error: orderError,
    movePeople,
    moveTasks,
  } = useScrumOrder(team, {
    enabled: !!data && data.scope !== 'self',
  });
  const [completing, setCompleting] = useState(false);
  const rows = useMemo(() => (data ? liveScrumRows(data.people, order) : []), [data, order]);

  // Without the order the table would come up in one order and then jump to
  // another, so it waits for it; if it cannot be had, the default order serves.
  if (!data || (data.scope !== 'self' && orderPending && !orderError)) {
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
    <ViewFrame wide>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <TeamFilter teams={data.teams} />
        {data.people.length > 0 && (
          <Button size="sm" className="sm:ml-auto" onClick={() => setCompleting(true)}>
            Complete scrum
          </Button>
        )}
      </div>

      {data.people.length === 0 ? (
        <EmptyState title="Nobody to go round yet" hint="Add people to a team you manage, in Organization, Teams." />
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
            {orderError ? (
              <p role="status" className={cn('text-xs', DANGER_TEXT)}>
                Could not load your order, so this is the usual one.
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">
                Drag the handles to put people and their tasks in your own order. Only you see it.
              </p>
            )}
          </div>
          <ScrumTable
            rows={rows}
            caption="Scrum: each person's lead, open tasks and what they finished since the last scrum"
            onMovePeople={movePeople}
            onMoveTasks={moveTasks}
          />
        </>
      )}

      <CompleteScrumDialog
        open={completing}
        onOpenChange={setCompleting}
        teamId={team}
        peopleCount={data.people.length}
        order={orderOf(rows)}
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
  order,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  teamId?: string;
  peopleCount: number;
  /** The table as it is on screen, which is what the saved scrum keeps. */
  order: ScrumOrder;
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
        await scrumsApi.complete({
          teamId,
          notes: notes.trim() || undefined,
          order,
        });
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
