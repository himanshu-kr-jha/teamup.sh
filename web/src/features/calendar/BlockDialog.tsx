import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { FormDialog } from '@/shared/components/FormDialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { calendarApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { blockProblem, fromInputValue, toInputValue, type CalendarItem } from './layout';

type Kind = 'focus' | 'open';

/** Your own focus time, or time you are marking as free for meetings. */
export function BlockDialog({
  open,
  item,
  slot,
  onOpenChange,
}: {
  open: boolean;
  /** Editing this one; otherwise a new one. */
  item: CalendarItem | null;
  slot: { start: Date; end: Date } | null;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [kind, setKind] = useState<Kind>('focus');
  const [title, setTitle] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');

  useEffect(() => {
    if (!open || !slot) return;
    setKind(item?.kind === 'open' ? 'open' : 'focus');
    setTitle(item?.title ?? '');
    setStart(toInputValue(slot.start));
    setEnd(toInputValue(slot.end));
  }, [open, item, slot]);

  const startDate = fromInputValue(start);
  const endDate = fromInputValue(end);
  const problem = blockProblem(startDate, endDate);

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={item ? 'Edit this time' : 'Block time'}
      description="Focus time tells people you would rather not be booked. Open time says you are free for meetings. Anyone can see that you have it, but only your manager can read the name."
      submitLabel={item ? 'Save' : 'Add to calendar'}
      disabledReason={problem ?? undefined}
      onSubmit={async () => {
        const body = {
          kind,
          title: title.trim(),
          startsAt: startDate!.toISOString(),
          endsAt: endDate!.toISOString(),
        };
        if (item?.id) await calendarApi.updateBlock(item.id, body);
        else await calendarApi.createBlock(body);
        await queryClient.invalidateQueries({ queryKey: ['calendar'] });
        toast.success(item ? 'Saved.' : kind === 'focus' ? 'Focus time added.' : 'Open time added.');
      }}
    >
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">What is it?</legend>
        <div className="grid grid-cols-2 gap-2">
          {(
            [
              ['focus', 'Focus time', 'For your own work'],
              ['open', 'Open for meetings', 'Free to be booked'],
            ] as const
          ).map(([value, label, hint]) => (
            <button
              key={value}
              type="button"
              aria-pressed={kind === value}
              onClick={() => setKind(value)}
              className={cn(
                'rounded-md border p-2 text-left text-sm transition-colors',
                kind === value ? 'border-primary bg-primary/10' : 'border-input hover:bg-accent'
              )}
            >
              <span className="block font-medium">{label}</span>
              <span className="block text-xs text-muted-foreground">{hint}</span>
            </button>
          ))}
        </div>
      </fieldset>

      <div className="space-y-2">
        <Label htmlFor="blockTitle">Name (optional)</Label>
        <Input id="blockTitle" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Writing the spec" />
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="min-w-0 space-y-2">
          <Label htmlFor="blockStart">From</Label>
          <Input
            id="blockStart"
            type="datetime-local"
            value={start}
            onChange={(e) => setStart(e.target.value)}
          />
        </div>
        <div className="min-w-0 space-y-2">
          <Label htmlFor="blockEnd">To</Label>
          <Input id="blockEnd" type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
        </div>
      </div>
      {problem && <p className="text-sm text-destructive" role="alert">{problem}</p>}
    </FormDialog>
  );
}
