import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { FormDialog } from '@/shared/components/FormDialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { calendarApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { blockProblem, fromInputValue, toInputValue, type CalendarItem } from './layout';

type Kind = 'focus' | 'open' | 'entry';

const KINDS: ReadonlyArray<readonly [Kind, string, string]> = [
  ['focus', 'Focus time', 'For your own work'],
  ['open', 'Open for meetings', 'Free to be booked'],
  ['entry', 'Entry', 'Something you did elsewhere'],
];

const ABOUT: Record<Kind, string> = {
  focus: 'Focus time tells people you would rather not be booked. Anyone can see that you have it, but only your manager can read the name.',
  open: 'Open time says you are free for meetings. Anyone can see that you have it, but only your manager can read the name.',
  entry: 'A record of what you did, such as a meeting held outside TeamUp. You and your manager can read it; to anyone else you were busy.',
};

/** Your own focus time, time you are marking as free for meetings, or an entry about what you did. */
export function BlockDialog({
  open,
  item,
  slot,
  startKind = 'focus',
  onOpenChange,
}: {
  open: boolean;
  /** Editing this one; otherwise a new one. */
  item: CalendarItem | null;
  slot: { start: Date; end: Date } | null;
  /** What a new one starts as. */
  startKind?: Kind;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [kind, setKind] = useState<Kind>('focus');
  const [title, setTitle] = useState('');
  const [details, setDetails] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');

  useEffect(() => {
    if (!open || !slot) return;
    setKind(item ? (item.kind === 'open' || item.kind === 'entry' ? item.kind : 'focus') : startKind);
    setTitle(item?.title ?? '');
    setDetails(item?.details ?? '');
    setStart(toInputValue(slot.start));
    setEnd(toInputValue(slot.end));
  }, [open, item, slot, startKind]);

  const startDate = fromInputValue(start);
  const endDate = fromInputValue(end);
  const problem = blockProblem(startDate, endDate) ?? (kind === 'entry' && !title.trim() ? 'Say what it was.' : null);

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={item ? (kind === 'entry' ? 'Edit this entry' : 'Edit this time') : kind === 'entry' ? 'Add an entry' : 'Block time'}
      description={ABOUT[kind]}
      submitLabel={item ? 'Save' : 'Add to calendar'}
      disabledReason={problem ?? undefined}
      onSubmit={async () => {
        const body = {
          kind,
          title: title.trim(),
          details: kind === 'entry' ? details.trim() : '',
          startsAt: startDate!.toISOString(),
          endsAt: endDate!.toISOString(),
        };
        if (item?.id) await calendarApi.updateBlock(item.id, body);
        else await calendarApi.createBlock(body);
        await queryClient.invalidateQueries({ queryKey: ['calendar'] });
        toast.success(item ? 'Saved.' : kind === 'focus' ? 'Focus time added.' : kind === 'open' ? 'Open time added.' : 'Entry added.');
      }}
    >
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">What is it?</legend>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {KINDS.map(([value, label, hint]) => (
            <button
              key={value}
              type="button"
              aria-pressed={kind === value}
              onClick={() => setKind(value)}
              className={cn(
                'flex flex-col justify-start rounded-md border p-2 text-left text-sm transition-colors',
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
        <Label htmlFor="blockTitle">{kind === 'entry' ? 'Title' : 'Name (optional)'}</Label>
        <Input
          id="blockTitle"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={kind === 'entry' ? 'Call with ACME about the pilot' : 'Writing the spec'}
          maxLength={255}
        />
      </div>

      {kind === 'entry' && (
        <div className="space-y-2">
          <Label htmlFor="blockDetails">Notes (optional)</Label>
          <Textarea
            id="blockDetails"
            value={details}
            onChange={(e) => setDetails(e.target.value)}
            rows={3}
            maxLength={5000}
            placeholder="What was decided, what you did, what comes next"
          />
        </div>
      )}

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
