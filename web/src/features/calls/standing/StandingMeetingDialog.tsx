import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2, UsersRound } from 'lucide-react';
import { PeoplePicker } from '@/shared/components/PeoplePicker';
import { FormDialog } from '@/shared/components/FormDialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { standingApi } from '@/shared/api/client';
import { cn } from '@/shared/utils/cn';
import {
  WEEKDAY_OPTIONS,
  audienceProblem,
  browserTimeZone,
  standingFormProblem,
  type AudienceForm,
  type StandingForm,
  type StandingMeeting,
} from './standing';

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
  const [audience, setAudience] = useState<AudienceForm>({ everyone: false, teamIds: [], personIds: [] });

  // What this person may choose from: everybody only for an admin, otherwise the
  // teams they manage and the people under them. The server checks it again.
  const { data: options, isPending: loadingOptions } = useQuery({
    queryKey: ['standing-audience-options'],
    queryFn: () => standingApi.audienceOptions().then((res) => res.data),
    enabled: open,
    staleTime: 60_000,
  });

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
    const a = meeting?.audience;
    setAudience(
      a
        ? { everyone: a.everyone, teamIds: a.teams.map((t) => t.id), personIds: a.people.map((p) => p.id) }
        : { everyone: false, teamIds: [], personIds: [] }
    );
  }, [open, meeting]);

  // A new meeting by an admin starts as everybody's, as standing meetings always were.
  useEffect(() => {
    if (open && !meeting && options?.everyone) setAudience((a) => ({ ...a, everyone: true }));
  }, [open, meeting, options?.everyone]);

  const zoneList = useMemo(() => zones(form.timeZone), [form.timeZone]);
  const current = { ...form, durationMinutes: Number(lengthText) };
  const problem = standingFormProblem(current) ?? audienceProblem(audience);

  const toggleTeam = (id: string) =>
    setAudience((a) => ({ ...a, teamIds: a.teamIds.includes(id) ? a.teamIds.filter((t) => t !== id) : [...a.teamIds, id] }));
  // Somebody already chosen stays on the list while editing, even if the editor
  // could not choose them now, so saving does not quietly drop them.
  const teamChoices = useMemo(() => {
    const list = [...(options?.teams ?? [])];
    for (const t of meeting?.audience?.teams ?? []) if (!list.some((o) => o.id === t.id)) list.push(t);
    return list;
  }, [options?.teams, meeting?.audience?.teams]);
  const peopleChoices = useMemo(() => {
    const list = [...(options?.people ?? [])];
    for (const p of meeting?.audience?.people ?? []) if (!list.some((o) => o.id === p.id)) list.push(p);
    return list;
  }, [options?.people, meeting?.audience?.people]);

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
      description="One room for a meeting that happens every day, such as the morning scrum. Only the people it is for see it and can join, and each day's attendance is saved."
      submitLabel={meeting ? 'Save' : 'Create'}
      disabledReason={problem ?? undefined}
      onSubmit={async () => {
        const body = {
          ...current,
          name: current.name.trim(),
          everyone: audience.everyone,
          teamIds: audience.everyone ? [] : audience.teamIds,
          personIds: audience.everyone ? [] : audience.personIds,
        };
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

      <fieldset className="space-y-2" aria-describedby="standingAudienceHelp">
        <legend className="flex items-center gap-1.5 text-sm font-medium">
          <UsersRound className="h-4 w-4" aria-hidden="true" />
          Who is it for
        </legend>
        {loadingOptions ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
            Loading teams and people
          </p>
        ) : (
          <>
            {options?.everyone && (
              <div className="flex flex-col gap-1.5 text-sm" role="radiogroup" aria-label="Who is it for">
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="standingAudience"
                    checked={audience.everyone}
                    onChange={() => setAudience((a) => ({ ...a, everyone: true }))}
                  />
                  Everybody in the organization
                </label>
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="standingAudience"
                    checked={!audience.everyone}
                    onChange={() => setAudience((a) => ({ ...a, everyone: false }))}
                  />
                  Chosen teams and people
                </label>
              </div>
            )}
            {!audience.everyone && (
              <div className="space-y-3">
                {teamChoices.length > 0 && (
                  <div className="space-y-1.5">
                    <p className="text-xs text-muted-foreground">Teams, with the teams beneath them</p>
                    <div className="flex flex-wrap gap-1.5">
                      {teamChoices.map((team) => {
                        const on = audience.teamIds.includes(team.id);
                        return (
                          <button
                            key={team.id}
                            type="button"
                            aria-pressed={on}
                            onClick={() => toggleTeam(team.id)}
                            className={cn(
                              'rounded-full border px-3 py-1 text-sm transition-colors',
                              on ? 'border-primary bg-primary text-primary-foreground' : 'border-input bg-background hover:bg-accent'
                            )}
                          >
                            {team.name}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )}
                <div className="space-y-1.5">
                  <p className="text-xs text-muted-foreground">People</p>
                  <PeoplePicker
                    people={peopleChoices}
                    selected={audience.personIds}
                    onChange={(ids) => setAudience((a) => ({ ...a, personIds: ids }))}
                    empty="There is nobody you can choose."
                    listClassName="max-h-40"
                  />
                </div>
              </div>
            )}
            <p id="standingAudienceHelp" className="text-xs text-muted-foreground">
              {audience.everyone
                ? 'Everybody sees it and can join.'
                : 'Only these people see it and can join; somebody joining a team later is in it at once. Anybody else with the link has to knock, and is let in for that day only.'}
            </p>
          </>
        )}
      </fieldset>

      {problem && open && form.name && <p className="text-xs text-muted-foreground">{problem}</p>}
    </FormDialog>
  );
}
