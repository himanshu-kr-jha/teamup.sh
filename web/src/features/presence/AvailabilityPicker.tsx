import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { FormDialog } from '@/shared/components/FormDialog';
import { presenceApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { PresenceDot } from './PresenceDot';
import type { PresenceStatus } from './usePresence';

/** What somebody can choose. In a call and offline are observed, not chosen. */
const choices: { value: PresenceStatus; label: string; hint: string }[] = [
  { value: 'available', label: 'Available', hint: 'Around and happy to be interrupted' },
  { value: 'busy', label: 'Busy', hint: 'Working on something; interrupt only if it matters' },
  { value: 'away', label: 'Away', hint: 'Not at the desk for a while' },
  { value: 'on_leave', label: 'On leave', hint: 'Off for a day or more' },
];

/**
 * Setting your own availability, and saying something about it.
 *
 * The note is what makes leave useful to anybody else: "back on the 14th" tells
 * a colleague whether to wait or to ask somebody else.
 */
export function AvailabilityPicker({
  open,
  onOpenChange,
  current,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  current?: { status: PresenceStatus; note?: string; until?: string };
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<PresenceStatus>(current?.status ?? 'available');
  const [note, setNote] = useState(current?.note ?? '');
  const [until, setUntil] = useState(current?.until?.slice(0, 10) ?? '');

  const needsDates = status === 'on_leave';

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Your availability"
      description="What colleagues see beside your name."
      submitLabel="Save"
      onSubmit={async () => {
        await presenceApi.setAvailability({
          status,
          note: note.trim() || undefined,
          // Only leave has an end worth recording.
          until: needsDates && until ? new Date(until).toISOString() : null,
        });
        queryClient.invalidateQueries({ queryKey: ['availability'] });
        toast.success('Availability updated.');
      }}
    >
      <div className="space-y-1">
        {choices.map((choice) => (
          <button
            key={choice.value}
            type="button"
            onClick={() => setStatus(choice.value)}
            className={cn(
              'flex w-full items-start gap-3 rounded-md border p-2.5 text-left transition-colors',
              status === choice.value
                ? 'border-primary bg-primary/5'
                : 'border-transparent hover:bg-accent'
            )}
          >
            <PresenceDot status={choice.value} className="mt-1 shrink-0" />
            <span className="min-w-0">
              <span className="block text-sm font-medium">{choice.label}</span>
              <span className="block text-xs text-muted-foreground">{choice.hint}</span>
            </span>
          </button>
        ))}
      </div>

      <div className="space-y-2">
        <Label htmlFor="availabilityNote">
          Note <span className="font-normal text-muted-foreground">(optional)</span>
        </Label>
        <Input
          id="availabilityNote"
          value={note}
          maxLength={200}
          onChange={(e) => setNote(e.target.value)}
          placeholder={
            status === 'on_leave'
              ? 'Back on the 14th — ask Priya for anything urgent'
              : status === 'busy'
                ? 'Heads down until lunch'
                : 'Anything colleagues should know'
          }
        />
      </div>

      {needsDates && (
        <div className="space-y-2">
          <Label htmlFor="availabilityUntil">
            Back on <span className="font-normal text-muted-foreground">(optional)</span>
          </Label>
          <Input
            id="availabilityUntil"
            type="date"
            value={until}
            onChange={(e) => setUntil(e.target.value)}
          />
        </div>
      )}
    </FormDialog>
  );
}

/** The button that opens it, showing where you currently stand. */
export function AvailabilityButton({
  current,
}: {
  current?: { status: PresenceStatus; note?: string; until?: string };
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        className="h-8 gap-2"
        onClick={() => setOpen(true)}
        aria-label="Set your availability"
      >
        <PresenceDot status={current?.status ?? 'available'} note={current?.note} />
        <span className="text-xs text-muted-foreground">Availability</span>
      </Button>
      {/* Remounted each time so it opens showing where you actually are. */}
      {open && <AvailabilityPicker open={open} onOpenChange={setOpen} current={current} />}
    </>
  );
}
