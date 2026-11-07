import { useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { FormDialog } from '@/shared/components/FormDialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { standingApi } from '@/shared/api/client';
import { cn } from '@/shared/utils/cn';
import { WEEKDAY_OPTIONS, browserTimeZone, standingFormProblem, type StandingForm, type StandingMeeting } from './standing';

const blank = (): StandingForm => ({
  name: '',
  timeOfDay: '09:30',
  timeZone: browserTimeZone(),
  weekdays: [1, 2, 3, 4, 5],
  durationMinutes: 30,
});

/** Every zone the browser knows, with the one already chosen always on the list. */
function zones(current: string): string[] {
  // Newer than the TypeScript library this project targets.
  const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
  const all = supported ? supported('timeZone') : [browserTimeZone(), 'UTC'];
  return all.includes(current) ? all : [current, ...all];
}

export function StandingMeetingDialog({
  open,
  meeting,
  onOpenChange,
}: {
  open: boolean;
  /** Editing this one; otherwise a new one. */
  meeting: StandingMeeting | null;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [form, setForm] = useState<StandingForm>(blank);
  const [lengthText, setLengthText] = useState('30');

  useEffect(() => {
    if (!open) return;
    const start = meeting ?? blank();
    setForm({
      name: start.name,
      timeOfDay: start.timeOfDay,
      timeZone: start.timeZone,
      weekdays: start.weekdays,
      durationMinutes: start.durationMinutes,
    });
    setLengthText(String(start.durationMinutes));
  }, [open, meeting]);

  const zoneList = useMemo(() => zones(form.timeZone), [form.timeZone]);
  const current = { ...form, durationMinutes: Number(lengthText) };
  const problem = standingFormProblem(current);

  const toggleDay = (day: number) =>
    setForm((f) => ({
      ...f,
      weekdays: f.weekdays.includes(day) ? f.weekdays.filter((d) => d !== day) : [...f.weekdays, day].sort((a, b) => a - b),
    }));

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={meeting ? 'Edit standing meeting' : 'New standing meeting'}
      description="One room for everybody, such as the morning scrum. Anyone in the organization can join it, and each day's attendance is saved."
      submitLabel={meeting ? 'Save' : 'Create'}
      disabledReason={problem ?? undefined}
      onSubmit={async () => {
        const body = { ...current, name: current.name.trim() };
        if (meeting) await standingApi.update(meeting.id, body);
        else await standingApi.create(body);
        await queryClient.invalidateQueries({ queryKey: ['standing-meetings'] });
        await queryClient.invalidateQueries({ queryKey: ['calendar'] });
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="standingName">Name</Label>
        <Input
          id="standingName"
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          placeholder="Morning scrum"
          autoFocus
        />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="min-w-0 space-y-2">
          <Label htmlFor="standingTime">Time</Label>
          <Input
            id="standingTime"
            type="time"
            value={form.timeOfDay}
            onChange={(e) => setForm({ ...form, timeOfDay: e.target.value })}
          />
        </div>
        <div className="min-w-0 space-y-2">
          <Label htmlFor="standingLength">Length in minutes</Label>
          <Input
            id="standingLength"
            type="number"
            min={1}
            max={480}
            value={lengthText}
            onChange={(e) => setLengthText(e.target.value)}
          />
        </div>
      </div>

      <div className="space-y-2">
        <Label htmlFor="standingZone">Time zone</Label>
        <select
          id="standingZone"
          value={form.timeZone}
          onChange={(e) => setForm({ ...form, timeZone: e.target.value })}
          className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
        >
          {zoneList.map((zone) => (
            <option key={zone} value={zone}>
              {zone}
            </option>
          ))}
        </select>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Days</legend>
        <div className="flex flex-wrap gap-1">
          {WEEKDAY_OPTIONS.map(({ value, label }) => {
            const on = form.weekdays.includes(value);
            return (
              <button
                key={value}
                type="button"
                aria-pressed={on}
                onClick={() => toggleDay(value)}
                className={cn(
                  'h-9 w-11 rounded-md border text-sm transition-colors',
                  on ? 'border-primary bg-primary text-primary-foreground' : 'border-input bg-background hover:bg-accent'
                )}
              >
                {label}
              </button>
            );
          })}
        </div>
      </fieldset>

      {problem && open && form.name && <p className="text-xs text-muted-foreground">{problem}</p>}
    </FormDialog>
  );
}
