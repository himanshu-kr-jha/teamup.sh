import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { History, Pencil, Plus, Trash2 } from 'lucide-react';
import { Badge } from '@/shared/components/ui/badge';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { holidaysApi, leaveApi, localToday, type Holiday, type LeavePerson, type LeaveRecord } from '@/shared/api/client';
import { useAuth } from '@/features/auth/AuthProvider';
import { PERMS, can } from '@/shared/permissions';
import { useToast } from '@/shared/hooks/useToast';
import { apiErrorMessage } from '@/shared/api/errors';
import { cn } from '@/shared/utils/cn';
import { DANGER_TEXT } from '@/features/tasks/dueDate';
import { EmptyState, ErrorState, LoadingState } from '@/features/tasks/dashboard/parts';
import {
  addDays,
  dayLabel,
  describeChange,
  describeLeave,
  draftFrom,
  leaveDraftBody,
  leaveDraftProblem,
  provenance,
  recordedAfterTheFact,
  splitLeave,
  type LeaveDraft,
} from './leave';

const EVERYBODY = 'all';

/**
 * Your leave, and the leave of whoever you manage, with the organization's
 * holidays. Attendance reads both, so nobody shows as absent while away. It
 * opens from your profile menu; administrators keep holidays, and read
 * everybody's leave, under Organization.
 */
export function LeavePage() {
  const today = localToday();
  const { user } = useAuth();
  const keepsHolidays = can(user, PERMS.orgSettings);
  const { data: people = [] } = useLeavePeople();
  const managesOthers = people.length > 1;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-6 p-3 md:p-6">
        <div className="min-w-0">
          <h1 className="text-xl font-bold">Leave</h1>
          <p className="text-sm text-muted-foreground">
            {managesOthers ? 'Your leave and the leave of the people you manage.' : 'Your leave.'} Nobody shows as
            absent from a stand-up while on leave or on a holiday, and it shows on the calendar.
          </p>
        </div>
        <LeaveBoard />
        <Holidays today={today} keeps={false} keptUnderOrganization={keepsHolidays} />
      </div>
    </div>
  );
}

function useLeavePeople() {
  return useQuery({
    queryKey: ['leave-people'],
    queryFn: () => leaveApi.people().then((res) => res.data),
    staleTime: 60_000,
  });
}

/**
 * The leave somebody may read, coming up and in the past year, with a way to add
 * and correct it. Whose it covers is the server's rule: your own, the people you
 * manage, and for an administrator everybody.
 */
export function LeaveBoard() {
  const today = localToday();
  const [person, setPerson] = useState(EVERYBODY);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<LeaveRecord | null>(null);
  const [removing, setRemoving] = useState<LeaveRecord | null>(null);
  const [historyOf, setHistoryOf] = useState<LeaveRecord | null>(null);

  const range = { from: addDays(today, -365), to: addDays(today, 730) };
  const { data: people = [] } = useLeavePeople();
  const managesOthers = people.length > 1;
  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['leave', range.from, range.to],
    queryFn: () => leaveApi.list(range).then((res) => res.data),
  });
  const shown = useMemo(
    () => (data ?? []).filter((l) => person === EVERYBODY || l.userId === person),
    [data, person]
  );
  const { upcoming, past } = splitLeave(shown, today);

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        {managesOthers ? (
          <Select value={person} onValueChange={setPerson}>
            <SelectTrigger className="h-9 w-full sm:w-64" aria-label="Show leave for">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={EVERYBODY}>Everybody</SelectItem>
              {people.map((p, i) => (
                <SelectItem key={p.id} value={p.id}>
                  {i === 0 ? `${p.name} (you)` : p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <span />
        )}
        <Button className="shrink-0" onClick={() => setAdding(true)}>
          <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
          Add leave
        </Button>
      </div>

      {isPending ? (
        <LoadingState label="Loading leave" />
      ) : !data ? (
        <ErrorState error={error} fallback="Could not load leave." onRetry={() => refetch()} />
      ) : (
        <>
          <LeaveSection
            title="Now and coming up"
            empty="No leave coming up."
            records={upcoming}
            today={today}
            named={managesOthers}
            onEdit={setEditing}
            onRemove={setRemoving}
            onHistory={setHistoryOf}
          />
          <LeaveSection
            title="Past year"
            empty="No leave in the past year."
            records={past}
            today={today}
            named={managesOthers}
            onEdit={setEditing}
            onRemove={setRemoving}
            onHistory={setHistoryOf}
          />
        </>
      )}

      {adding && <LeaveDialog people={people} today={today} onClose={() => setAdding(false)} />}
      {editing && <LeaveDialog people={people} today={today} record={editing} onClose={() => setEditing(null)} />}
      {removing && <RemoveDialog record={removing} today={today} onClose={() => setRemoving(null)} />}
      {historyOf && <HistoryDialog record={historyOf} today={today} onClose={() => setHistoryOf(null)} />}
    </div>
  );
}

function LeaveSection({
  title,
  empty,
  records,
  today,
  named,
  onEdit,
  onRemove,
  onHistory,
}: {
  title: string;
  empty: string;
  records: LeaveRecord[];
  today: string;
  named: boolean;
  onEdit: (l: LeaveRecord) => void;
  onRemove: (l: LeaveRecord) => void;
  onHistory: (l: LeaveRecord) => void;
}) {
  return (
    <section aria-label={title} className="space-y-2">
      <h2 className="text-sm font-semibold">{title}</h2>
      {records.length === 0 ? (
        <EmptyState title={empty} />
      ) : (
        <ul className="divide-y divide-border rounded-lg border border-border bg-card">
          {records.map((l) => (
            <li key={l.id} className="flex items-start gap-2 px-3 py-2.5">
              <div className="min-w-0 flex-1 space-y-0.5">
                <p className="break-anywhere text-sm font-medium">
                  {named && <span>{l.userName}: </span>}
                  {describeLeave(l, today)}
                  {l.startsOn <= today && (l.endsOn === null || l.endsOn >= today) && (
                    <Badge variant="secondary" className="ml-2 bg-sky-100 align-middle text-sky-800 dark:bg-sky-950 dark:text-sky-200">
                      Now
                    </Badge>
                  )}
                </p>
                {l.note && <p className="break-anywhere text-sm text-muted-foreground">{l.note}</p>}
                <p className="text-xs text-muted-foreground">
                  {provenance(l)}
                  {recordedAfterTheFact(l) && (
                    <span className="font-medium text-amber-700 dark:text-amber-400">, after it had started</span>
                  )}
                </p>
              </div>
              <div className="flex shrink-0 items-center">
                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={`History of ${describeLeave(l, today)}`} title="History" onClick={() => onHistory(l)}>
                  <History className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={`Change ${describeLeave(l, today)}`} title="Change" onClick={() => onEdit(l)}>
                  <Pencil className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={`Remove ${describeLeave(l, today)}`} title="Remove" onClick={() => onRemove(l)}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function useRefreshLeave() {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: ['leave'] });
    void queryClient.invalidateQueries({ queryKey: ['attendance'] });
    void queryClient.invalidateQueries({ queryKey: ['availability'] });
    void queryClient.invalidateQueries({ queryKey: ['calendar'] });
  };
}

function LeaveDialog({
  people,
  today,
  record,
  onClose,
}: {
  people: LeavePerson[];
  today: string;
  record?: LeaveRecord;
  onClose: () => void;
}) {
  const toast = useToast();
  const refresh = useRefreshLeave();
  const [who, setWho] = useState(record?.userId ?? people[0]?.id ?? '');
  const [draft, setDraft] = useState<LeaveDraft>(
    record ? draftFrom(record) : { half: false, startsOn: today, endsOn: today, part: 'morning', note: '' }
  );
  const set = (changes: Partial<LeaveDraft>) => setDraft((d) => ({ ...d, ...changes }));
  const problem = leaveDraftProblem(draft);
  const forSomebodyElse = record ? record.userId !== people[0]?.id : who !== people[0]?.id;

  return (
    <FormDialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={record ? 'Change leave' : 'Add leave'}
      description={
        record && forSomebodyElse
          ? `${record.userName}'s leave. They are told about the change.`
          : 'Days already gone can be added too. The change is kept with your name on it.'
      }
      submitLabel={record ? 'Save' : 'Add leave'}
      disabledReason={problem ?? undefined}
      onSubmit={async () => {
        const body = leaveDraftBody(draft);
        if (record) await leaveApi.change(record.id, body);
        else await leaveApi.add({ ...body, userId: who });
        refresh();
        toast.success(record ? 'Leave changed.' : 'Leave added.');
      }}
    >
      {!record && people.length > 1 && (
        <div className="space-y-2">
          <Label htmlFor="leavePerson">Whose leave</Label>
          <Select value={who} onValueChange={setWho}>
            <SelectTrigger id="leavePerson">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {people.map((p, i) => (
                <SelectItem key={p.id} value={p.id}>
                  {i === 0 ? `${p.name} (you)` : p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      <div className="grid grid-cols-2 gap-1 rounded-md bg-muted p-1" role="group" aria-label="How long">
        {[
          { half: false, label: 'Whole days' },
          { half: true, label: 'Half a day' },
        ].map((option) => (
          <button
            key={option.label}
            type="button"
            aria-pressed={draft.half === option.half}
            onClick={() => set({ half: option.half })}
            className={cn(
              'rounded px-2 py-1.5 text-sm font-medium transition-colors',
              draft.half === option.half ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'
            )}
          >
            {option.label}
          </button>
        ))}
      </div>

      {draft.half ? (
        <div className="grid grid-cols-2 gap-2">
          <div className="min-w-0 space-y-2">
            <Label htmlFor="leaveDay">Day</Label>
            <Input id="leaveDay" type="date" value={draft.startsOn} onChange={(e) => set({ startsOn: e.target.value })} />
          </div>
          <div className="min-w-0 space-y-2">
            <Label htmlFor="leaveHalf">Which half</Label>
            <Select value={draft.part} onValueChange={(v) => set({ part: v as LeaveDraft['part'] })}>
              <SelectTrigger id="leaveHalf">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="morning">Morning</SelectItem>
                <SelectItem value="afternoon">Afternoon</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-2">
          <div className="min-w-0 space-y-2">
            <Label htmlFor="leaveFirst">First day</Label>
            <Input
              id="leaveFirst"
              type="date"
              value={draft.startsOn}
              onChange={(e) => {
                const startsOn = e.target.value;
                // Moving the start past the end takes the end along.
                set({ startsOn, endsOn: draft.endsOn && draft.endsOn < startsOn ? startsOn : draft.endsOn });
              }}
            />
          </div>
          <div className="min-w-0 space-y-2">
            <Label htmlFor="leaveLast">Last day</Label>
            <Input id="leaveLast" type="date" value={draft.endsOn} min={draft.startsOn} onChange={(e) => set({ endsOn: e.target.value })} />
          </div>
          <p className="col-span-2 text-xs text-muted-foreground">Leave the last day empty if there is no return date yet.</p>
        </div>
      )}

      <div className="space-y-2">
        <Label htmlFor="leaveNote">
          Note <span className="font-normal text-muted-foreground">(optional)</span>
        </Label>
        <Input
          id="leaveNote"
          value={draft.note}
          maxLength={200}
          onChange={(e) => set({ note: e.target.value })}
          placeholder="Family wedding"
        />
        <p className="text-xs text-muted-foreground">Only the person, their managers and administrators see it.</p>
      </div>

      {problem && draft.startsOn && (
        <p role="alert" className={cn('text-sm', DANGER_TEXT)}>
          {problem}
        </p>
      )}
    </FormDialog>
  );
}

function RemoveDialog({ record, today, onClose }: { record: LeaveRecord; today: string; onClose: () => void }) {
  const toast = useToast();
  const refresh = useRefreshLeave();
  return (
    <FormDialog
      open
      onOpenChange={(open) => !open && onClose()}
      title="Remove this leave?"
      description={`${record.userName}, ${describeLeave(record, today)}. Those days count as if there had been no leave. Its history is kept.`}
      submitLabel="Remove"
      destructive
      onSubmit={async () => {
        await leaveApi.remove(record.id);
        refresh();
        toast.success('Leave removed.');
      }}
    />
  );
}

function HistoryDialog({ record, today, onClose }: { record: LeaveRecord; today: string; onClose: () => void }) {
  const { data, isPending, error, refetch } = useQuery({
    queryKey: ['leave-history', record.id],
    queryFn: () => leaveApi.history(record.id).then((res) => res.data),
  });
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>History</DialogTitle>
          <DialogDescription>
            {record.userName}, {describeLeave(record, today)}
          </DialogDescription>
        </DialogHeader>
        {isPending ? (
          <LoadingState label="Loading the history" />
        ) : !data ? (
          <ErrorState error={error} fallback="Could not load the history." onRetry={() => refetch()} />
        ) : (
          <ol className="space-y-2">
            {data.map((c, i) => (
              <li key={i} className="text-sm">
                <p className="break-anywhere">{describeChange(c, today)}</p>
                <p className="text-xs tabular-nums text-muted-foreground">
                  {new Date(c.at).toLocaleString('en-US', { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })}
                  {c.note && ` · ${c.note}`}
                </p>
              </li>
            ))}
          </ol>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function Holidays({
  today,
  keeps,
  keptUnderOrganization = false,
}: {
  today: string;
  /** Whether this list is where they are added and removed. */
  keeps: boolean;
  /** Read here, but this person keeps them under Organization. */
  keptUnderOrganization?: boolean;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const year = today.slice(0, 4);
  const range = { from: `${year}-01-01`, to: `${Number(year) + 1}-12-31` };
  const [date, setDate] = useState('');
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState<Holiday | null>(null);
  const { data, isPending, error, refetch } = useQuery({
    queryKey: ['holidays', range.from, range.to],
    queryFn: () => holidaysApi.list(range).then((res) => res.data),
  });
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['holidays'] });
    void queryClient.invalidateQueries({ queryKey: ['attendance'] });
    void queryClient.invalidateQueries({ queryKey: ['calendar'] });
  };

  return (
    <section aria-label="Holidays" className="space-y-2">
      <h2 className="text-sm font-semibold">Holidays</h2>
      <p className="text-sm text-muted-foreground">
        Days the whole organization is off.{' '}
        {keeps ? (
          'They show on everybody\'s calendar, and nobody is expected at a stand-up on them.'
        ) : keptUnderOrganization ? (
          <>
            You keep this list under{' '}
            <Link to="/organization?tab=leave" className="underline underline-offset-2 hover:text-foreground">
              Organization, Leave &amp; holidays
            </Link>
            .
          </>
        ) : (
          'An administrator keeps this list.'
        )}
      </p>
      {keeps && (
        <form
          className="flex flex-col gap-2 sm:flex-row sm:items-end"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!date || !name.trim() || saving) return;
            setSaving(true);
            try {
              await holidaysApi.add({ date, name: name.trim() });
              setDate('');
              setName('');
              refresh();
              toast.success('Holiday added.');
            } catch (err) {
              toast.error(apiErrorMessage(err, 'Could not add the holiday.'));
            } finally {
              setSaving(false);
            }
          }}
        >
          <div className="min-w-0 space-y-1 sm:w-44">
            <Label htmlFor="holidayDate" className="text-xs">
              Date
            </Label>
            <Input id="holidayDate" type="date" className="h-9" value={date} onChange={(e) => setDate(e.target.value)} />
          </div>
          <div className="min-w-0 flex-1 space-y-1">
            <Label htmlFor="holidayName" className="text-xs">
              Name
            </Label>
            <Input id="holidayName" className="h-9" value={name} maxLength={120} placeholder="Diwali" onChange={(e) => setName(e.target.value)} />
          </div>
          <Button type="submit" variant="outline" className="h-9" disabled={!date || !name.trim() || saving}>
            Add holiday
          </Button>
        </form>
      )}
      {isPending ? (
        <LoadingState label="Loading holidays" />
      ) : !data ? (
        <ErrorState error={error} fallback="Could not load holidays." onRetry={() => refetch()} />
      ) : data.length === 0 ? (
        <EmptyState title="No holidays this year or next." />
      ) : (
        <ul className="divide-y divide-border rounded-lg border border-border bg-card">
          {data.map((h) => (
            <li key={h.id} className={cn('flex items-center gap-2 px-3 py-2 text-sm', h.date < today && 'text-muted-foreground')}>
              <span className="w-36 shrink-0 tabular-nums">{dayLabel(h.date, today)}</span>
              <span className="min-w-0 flex-1 break-anywhere">{h.name}</span>
              {keeps && (
                <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" aria-label={`Remove ${h.name}`} title="Remove" onClick={() => setRemoving(h)}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      {removing && (
        <FormDialog
          open
          onOpenChange={(open) => !open && setRemoving(null)}
          title={`Remove ${removing.name}?`}
          description={`${dayLabel(removing.date, today)} becomes an ordinary day, so anybody who missed a stand-up then counts as absent.`}
          submitLabel="Remove"
          destructive
          onSubmit={async () => {
            await holidaysApi.remove(removing.id);
            refresh();
            toast.success('Holiday removed.');
          }}
        />
      )}
    </section>
  );
}
