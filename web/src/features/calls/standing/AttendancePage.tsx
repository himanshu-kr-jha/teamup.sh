import { useState } from 'react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { ChevronLeft, Download, History, Video } from 'lucide-react';
import { Badge } from '@/shared/components/ui/badge';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { fieldWorkApi, standingApi } from '@/shared/api/client';
import { Switch } from '@/shared/components/ui/switch';
import { useAuth } from '@/features/auth/AuthProvider';
import { PERMS, can } from '@/shared/permissions';
import {
  CameraDetail,
  FieldDetail,
  LateJoinDetail,
  ReviewButton,
  ReviewDetail,
  STATUS_LABEL,
  reviewable,
} from './AttendanceDetails';
import { cn, formatTime } from '@/shared/utils/cn';
import { DANGER_TEXT } from '@/features/tasks/dueDate';
import { EmptyState, ErrorState, LoadingState } from '@/features/tasks/dashboard/parts';
import { useDashboard } from '@/features/tasks/dashboard/useDashboard';
import { CallLogDialog } from '../CallLogDialog';
import { formatMinutes } from '../callLog';
import {
  attendanceRangeProblem,
  attendanceSummary,
  defaultAttendanceRange,
  describeSchedule,
  orderAttendance,
  type AttendanceDay,
  type AttendancePerson,
  type AttendanceStatus,
} from './standing';

const ALL_TEAMS = 'all';

const STATUS_STYLE: Record<AttendanceStatus, string> = {
  present: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-200',
  absent: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-200',
  unknown: 'bg-muted text-muted-foreground',
  on_leave: 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-200',
  holiday: 'bg-muted text-muted-foreground',
  field_work: 'bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-200',
  review: 'bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200',
};

/**
 * Who came to a standing meeting, day by day. Scope is entirely the server's: an
 * admin gets everybody, a manager the people they answer for, anybody else
 * themselves. This page draws whatever comes back.
 */
export function AttendancePage() {
  const { id = '' } = useParams<{ id: string }>();
  const [params, setParams] = useSearchParams();

  const update = (changes: Record<string, string | null>) =>
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        for (const [key, value] of Object.entries(changes)) {
          if (value) next.set(key, value);
          else next.delete(key);
        }
        return next;
      },
      { replace: true }
    );

  const { data: meetings, isPending: loadingMeetings } = useQuery({
    queryKey: ['standing-meetings'],
    queryFn: () => standingApi.list().then((res) => res.data),
    staleTime: 30_000,
  });
  const meeting = meetings?.find((m) => m.id === id);

  // Today is the meeting's today, once it is known whose that is. An archived meeting
  // is not in the list, and falls back on yours.
  const fallback = defaultAttendanceRange(new Date(), meeting?.timeZone);
  const range = { from: params.get('from') || fallback.from, to: params.get('to') || fallback.to };
  const team = params.get('team') ?? undefined;
  const problem = attendanceRangeProblem(range);

  // The team filter's options are the same ones the task dashboard offers,
  // which are empty for somebody who answers for nobody.
  const { data: dashboard } = useDashboard(undefined);
  const teams = dashboard?.teams ?? [];

  const { user } = useAuth();
  const reviewer = can(user, PERMS.attendance);
  // Attendance is for owners, admins and whoever is given it (16 November).
  const sees = reviewer || can(user, PERMS.attendanceView) || can(user, PERMS.meetingsAll);
  const { data, error, isPending, isFetching, refetch } = useQuery({
    queryKey: ['attendance', id, range.from, range.to, team ?? 'all'],
    queryFn: () => standingApi.attendance(id, { ...range, teamId: team }).then((res) => res.data),
    enabled: !problem && !loadingMeetings && sees,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
  const days = [...(data ?? [])].sort((a, b) => b.date.localeCompare(a.date));
  const exportParams = { ...range, teamId: team };

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl space-y-4 p-3 md:p-6">
        <div>
          <Button variant="ghost" size="sm" className="-ml-2 mb-1" asChild>
            <Link to="/calls">
              <ChevronLeft className="mr-1 h-4 w-4" aria-hidden="true" />
              Calls
            </Link>
          </Button>
          <h1 className="break-anywhere text-xl font-bold">{meeting?.name ?? 'Attendance'}</h1>
          <p className="text-sm text-muted-foreground">
            {meeting ? `${describeSchedule(meeting)}. ` : ''}Times below are in your time zone.
            {sees && (
              <>
                {' '}
                <Link to="/field-work" className="underline underline-offset-2 hover:text-foreground">
                  Field work photos
                </Link>
              </>
            )}
          </p>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div className="grid grid-cols-2 gap-2">
            <div className="min-w-0 space-y-1">
              <Label htmlFor="attendanceFrom" className="text-xs">
                From
              </Label>
              <Input
                id="attendanceFrom"
                type="date"
                className="h-9"
                value={range.from}
                max={range.to}
                onChange={(e) => update({ from: e.target.value })}
              />
            </div>
            <div className="min-w-0 space-y-1">
              <Label htmlFor="attendanceTo" className="text-xs">
                To
              </Label>
              <Input
                id="attendanceTo"
                type="date"
                className="h-9"
                value={range.to}
                min={range.from}
                onChange={(e) => update({ to: e.target.value })}
              />
            </div>
          </div>
          <div className="flex flex-wrap items-end gap-2 sm:ml-auto">
            {!problem && (
              <>
                <Button variant="outline" size="sm" className="h-9" asChild>
                  <a href={standingApi.exportUrl(id, { ...exportParams, format: 'xlsx' })} download>
                    <Download className="mr-1.5 h-4 w-4" aria-hidden="true" />
                    Excel
                  </a>
                </Button>
                <Button variant="outline" size="sm" className="h-9" asChild>
                  <a href={standingApi.exportUrl(id, { ...exportParams, format: 'csv' })} download>
                    <Download className="mr-1.5 h-4 w-4" aria-hidden="true" />
                    CSV
                  </a>
                </Button>
              </>
            )}
          {teams.length > 0 && (
            <Select value={team ?? ALL_TEAMS} onValueChange={(v) => update({ team: v === ALL_TEAMS ? null : v })}>
              <SelectTrigger className="h-9 w-full sm:w-48" aria-label="Filter by team">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_TEAMS}>All teams</SelectItem>
                {teams.map((t) => (
                  <SelectItem key={t.id} value={t.id}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          </div>
        </div>

        <FieldWorkSwitch />

        {!sees ? (
          <EmptyState
            title="Attendance is not for you to see"
            hint="Attendance is for administrators and whoever is given See attendance under Roles."
          />
        ) : problem ? (
          <p role="alert" className={cn('text-sm', DANGER_TEXT)}>
            {problem}
          </p>
        ) : isPending ? (
          <LoadingState label="Loading attendance" />
        ) : !data ? (
          <ErrorState error={error} fallback="Could not load attendance." onRetry={() => refetch()} />
        ) : days.length === 0 ? (
          <EmptyState title="Nothing in these dates" hint="This meeting did not run on these days, or has not yet." />
        ) : (
          <div className={cn('space-y-3', isFetching && 'opacity-70')}>
            {days.map((day) => (
              <DayCard key={day.date} day={day} meetingId={id} reviewer={reviewer} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function DayCard({ day, meetingId, reviewer }: { day: AttendanceDay; meetingId: string; reviewer: boolean }) {
  const [showingLog, setShowingLog] = useState(false);
  const people = orderAttendance(day.people);
  const summary = day.attendanceRecorded ? attendanceSummary(day) : 'Attendance could not be recorded';

  return (
    <section className="rounded-lg border border-border bg-card" aria-label={`Attendance on ${day.date}`}>
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-3 py-2">
        <h2 className="text-sm font-semibold">{dayTitle(day.date)}</h2>
        {day.holiday && (
          <Badge variant="secondary" className="break-anywhere">
            Holiday: {day.holiday}
          </Badge>
        )}
        {day.scheduledStart && (
          <span className="text-xs tabular-nums text-muted-foreground">Starts {formatTime(day.scheduledStart)}</span>
        )}
        {day.attendFrom && day.attendUntil && (
          <span className="text-xs tabular-nums text-muted-foreground">
            Counts {formatTime(day.attendFrom)} to {formatTime(day.attendUntil)}
          </span>
        )}
        {day.cameraRequired && (
          <Badge variant="outline" className="gap-1 text-xs font-normal">
            <Video className="h-3 w-3" aria-hidden="true" />
            Cameras required
          </Badge>
        )}
        <span className="ml-auto text-xs tabular-nums text-muted-foreground">{summary}</span>
        {day.callId && day.canReadLog && (
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            aria-label={`Show the call log for ${dayTitle(day.date)}`}
            title="Call log"
            onClick={() => setShowingLog(true)}
          >
            <History className="h-4 w-4" />
          </Button>
        )}
      </header>

      {!day.attendanceRecorded && !day.holiday && (
        <p role="note" className="border-b border-border bg-muted/50 px-3 py-2 text-sm">
          People pressed Join on this day, but the media server's connection reports never arrived. So
          nobody can be marked present or absent, and everybody shows as unknown.
        </p>
      )}

      {people.length === 0 ? (
        <p className="px-3 py-4 text-sm text-muted-foreground">Nobody to show on this day.</p>
      ) : (
        <>
          {/* A table on a computer and a list on a phone, since six columns do not fit one. */}
          <table className="hidden w-full text-sm sm:table">
            <thead>
              <tr className="border-b border-border text-xs text-muted-foreground">
                <th scope="col" className="px-3 py-2 text-left font-medium">Person</th>
                <th scope="col" className="px-2 py-2 text-left font-medium">Status</th>
                <th scope="col" className="px-2 py-2 text-right font-medium">First join</th>
                <th scope="col" className="px-2 py-2 text-right font-medium">Last leave</th>
                <th scope="col" className="px-2 py-2 text-right font-medium">Minutes present</th>
                <th scope="col" className="px-3 py-2 text-right font-medium">Minutes late</th>
                {reviewer && (
                  <th scope="col" className="px-3 py-2 text-right font-medium">
                    <span className="sr-only">Decide</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {people.map((person) => (
                <tr key={person.userId}>
                  <td className="break-anywhere px-3 py-2">
                    {person.name}
                    <PersonDetails person={person} />
                  </td>
                  <td className="px-2 py-2">
                    <StatusBadge person={person} />
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums">{clock(person.firstJoinAt)}</td>
                  <td className="px-2 py-2 text-right tabular-nums">{lastLeave(person)}</td>
                  <td className="px-2 py-2 text-right tabular-nums">{minutes(person, person.minutesPresent)}</td>
                  <td
                    className={cn(
                      'px-3 py-2 text-right tabular-nums',
                      attended(person) && person.minutesLate > 0 && DANGER_TEXT
                    )}
                  >
                    {minutes(person, person.minutesLate)}
                  </td>
                  {reviewer && (
                    <td className="px-3 py-2 text-right">
                      {reviewable(person) && <ReviewButton meetingId={meetingId} day={day} person={person} />}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>

          <ul className="divide-y divide-border sm:hidden">
            {people.map((person) => (
              <li key={person.userId} className="px-3 py-2 text-sm">
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 break-anywhere font-medium">{person.name}</span>
                  <StatusBadge person={person} />
                </div>
                <PersonDetails person={person} />
                {attended(person) && (
                  <p className="mt-0.5 text-xs tabular-nums text-muted-foreground">
                    {clock(person.firstJoinAt)} to {lastLeave(person)}, {formatMinutes(person.minutesPresent)}
                    {person.minutesLate > 0 && (
                      <span className={DANGER_TEXT}>, {formatMinutes(person.minutesLate)} late</span>
                    )}
                  </p>
                )}
                {reviewer && reviewable(person) && (
                  <div className="mt-1.5">
                    <ReviewButton meetingId={meetingId} day={day} person={person} />
                  </div>
                )}
              </li>
            ))}
          </ul>
        </>
      )}

      {day.callId && day.canReadLog && <CallLogDialog callId={day.callId} open={showingLog} onOpenChange={setShowingLog} />}
    </section>
  );
}

const HALF: Record<string, string> = { morning: 'Morning off', afternoon: 'Afternoon off' };

/**
 * Who recorded somebody's leave and when. Leave recorded once the meeting had
 * started is called out, so a past absence is never quietly turned into leave.
 */
function LeaveDetail({ person }: { person: AttendancePerson }) {
  const leave = person.leave;
  if (person.status !== 'on_leave' || !leave) return null;
  const when = new Date(leave.recordedAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  const who = leave.recordedBy ? leave.recordedBy.name : 'somebody who has since left';
  return (
    <span className="mt-0.5 block text-xs text-muted-foreground">
      {HALF[leave.part] ? `${HALF[leave.part]}. ` : ''}Added by {who} on {when}
      {leave.afterTheDay && <span className="font-medium text-amber-700 dark:text-amber-400">, after the meeting</span>}
    </span>
  );
}

function StatusBadge({ person }: { person: AttendancePerson }) {
  // Late is present, said as late so it reads at a glance.
  const late = person.status === 'present' && person.minutesLate > 0;
  return (
    <Badge
      variant="secondary"
      className={cn(
        'shrink-0 whitespace-nowrap',
        late ? 'bg-orange-100 text-orange-800 dark:bg-orange-950 dark:text-orange-200' : STATUS_STYLE[person.status]
      )}
    >
      {late ? 'Late' : STATUS_LABEL[person.status]}
    </Badge>
  );
}

/** The date is the meeting's own day, so it is read as a day and not as an instant. */
function dayTitle(date: string): string {
  return new Date(`${date}T00:00:00`).toLocaleDateString('en-US', {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

function clock(at: string | null): string {
  return at ? formatTime(at) : '-';
}

/** Somebody who was in the room, whatever the day ended up saying. */
function attended(person: AttendancePerson): boolean {
  return person.firstJoinAt !== null && ['present', 'review'].includes(person.review?.was ?? person.status);
}

function PersonDetails({ person }: { person: AttendancePerson }) {
  return (
    <>
      <LeaveDetail person={person} />
      <FieldDetail person={person} />
      <LateJoinDetail person={person} />
      <CameraDetail person={person} />
      <ReviewDetail person={person} />
    </>
  );
}

/**
 * Whether field work counts for stand-ups in this organization. Only whoever
 * changes organization settings sees the switch.
 */
function FieldWorkSwitch() {
  const queryClient = useQueryClient();
  const { data } = useQuery({
    queryKey: ['field-work-settings'],
    queryFn: () => fieldWorkApi.settings().then((res) => res.data),
    staleTime: 60_000,
  });
  const change = useMutation({
    mutationFn: (enabled: boolean) => fieldWorkApi.setEnabled(enabled),
    onSuccess: (res) => queryClient.setQueryData(['field-work-settings'], res.data),
  });
  if (!data?.mayChange) return null;
  return (
    <div className="flex items-start gap-3 rounded-lg border border-border bg-card px-3 py-2">
      <Switch
        id="fieldWork"
        checked={data.enabled}
        disabled={change.isPending}
        onCheckedChange={(on) => change.mutate(on)}
        className="mt-0.5"
      />
      <Label htmlFor="fieldWork" className="cursor-pointer text-sm font-normal leading-snug">
        <span className="font-medium">Field work check-ins</span>
        <span className="block text-muted-foreground">
          Somebody working away sends a photo and their location from the app, and is in the field rather than absent
          that day.
        </span>
      </Label>
    </div>
  );
}

function lastLeave(person: AttendancePerson): string {
  if (!attended(person)) return '-';
  return person.lastLeaveAt ? formatTime(person.lastLeaveAt) : 'Still there';
}

function minutes(person: AttendancePerson, value: number): string {
  return attended(person) ? String(value) : '-';
}

