import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { ChevronLeft, History } from 'lucide-react';
import { Badge } from '@/shared/components/ui/badge';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { standingApi } from '@/shared/api/client';
import { cn, formatTime } from '@/shared/utils/cn';
import { DANGER_TEXT } from '@/features/tasks/dueDate';
import { EmptyState, ErrorState, LoadingState } from '@/features/tasks/dashboard/parts';
import { useDashboard } from '@/features/tasks/dashboard/useDashboard';
import { CallLogDialog } from '../CallLogDialog';
import { formatMinutes } from '../callLog';
import {
  attendanceCounts,
  attendanceRangeProblem,
  defaultAttendanceRange,
  describeSchedule,
  orderAttendance,
  type AttendanceDay,
  type AttendancePerson,
  type AttendanceStatus,
} from './standing';

const ALL_TEAMS = 'all';

const STATUS_LABEL: Record<AttendanceStatus, string> = { present: 'Present', absent: 'Absent', unknown: 'Unknown' };

const STATUS_STYLE: Record<AttendanceStatus, string> = {
  present: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-200',
  absent: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-200',
  unknown: 'bg-muted text-muted-foreground',
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

  const { data, error, isPending, isFetching, refetch } = useQuery({
    queryKey: ['attendance', id, range.from, range.to, team ?? 'all'],
    queryFn: () => standingApi.attendance(id, { ...range, teamId: team }).then((res) => res.data),
    enabled: !problem && !loadingMeetings,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
  const days = [...(data ?? [])].sort((a, b) => b.date.localeCompare(a.date));

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

        {problem ? (
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
              <DayCard key={day.date} day={day} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function DayCard({ day }: { day: AttendanceDay }) {
  const [showingLog, setShowingLog] = useState(false);
  const counts = attendanceCounts(day.people);
  const people = orderAttendance(day.people);
  const summary = day.attendanceRecorded
    ? `${counts.present} present, ${counts.absent} absent`
    : 'Attendance could not be recorded';

  return (
    <section className="rounded-lg border border-border bg-card" aria-label={`Attendance on ${day.date}`}>
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-3 py-2">
        <h2 className="text-sm font-semibold">{dayTitle(day.date)}</h2>
        {day.scheduledStart && (
          <span className="text-xs tabular-nums text-muted-foreground">Starts {formatTime(day.scheduledStart)}</span>
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

      {!day.attendanceRecorded && (
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
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {people.map((person) => (
                <tr key={person.userId}>
                  <td className="break-anywhere px-3 py-2">{person.name}</td>
                  <td className="px-2 py-2">
                    <StatusBadge status={person.status} />
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums">{clock(person.firstJoinAt)}</td>
                  <td className="px-2 py-2 text-right tabular-nums">{lastLeave(person)}</td>
                  <td className="px-2 py-2 text-right tabular-nums">{minutes(person, person.minutesPresent)}</td>
                  <td
                    className={cn(
                      'px-3 py-2 text-right tabular-nums',
                      person.status === 'present' && person.minutesLate > 0 && DANGER_TEXT
                    )}
                  >
                    {minutes(person, person.minutesLate)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <ul className="divide-y divide-border sm:hidden">
            {people.map((person) => (
              <li key={person.userId} className="px-3 py-2 text-sm">
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 break-anywhere font-medium">{person.name}</span>
                  <StatusBadge status={person.status} />
                </div>
                {person.status === 'present' && (
                  <p className="mt-0.5 text-xs tabular-nums text-muted-foreground">
                    {clock(person.firstJoinAt)} to {lastLeave(person)}, {formatMinutes(person.minutesPresent)}
                    {person.minutesLate > 0 && (
                      <span className={DANGER_TEXT}>, {formatMinutes(person.minutesLate)} late</span>
                    )}
                  </p>
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

function StatusBadge({ status }: { status: AttendanceStatus }) {
  return (
    <Badge variant="secondary" className={cn('shrink-0', STATUS_STYLE[status])}>
      {STATUS_LABEL[status]}
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

function lastLeave(person: AttendancePerson): string {
  if (person.status !== 'present') return '-';
  return person.lastLeaveAt ? formatTime(person.lastLeaveAt) : 'Still there';
}

function minutes(person: AttendancePerson, value: number): string {
  return person.status === 'present' ? String(value) : '-';
}

