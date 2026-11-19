import { useMemo, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { ChevronLeft, MapPin } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { SearchSelect, usePersonOptions } from '@/shared/components/SearchSelect';
import { byName } from '@/shared/utils/peopleSearch';
import { fieldWorkApi, orgApi, type FieldCheckin } from '@/shared/api/client';
import { formatTime } from '@/shared/utils/cn';
import { EmptyState, ErrorState, LoadingState } from '@/features/tasks/dashboard/parts';
import { mapLink } from './AttendanceDetails';
import { coordinates } from './FieldWork';

const EVERYBODY = 'all';

function day(offset: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function dayTitle(date: string): string {
  return new Date(`${date}T00:00:00`).toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' });
}

/**
 * Field work check-ins in one place: your own, with their photos and where they
 * were taken; and for whoever sees attendance, everybody's, by person and date.
 */
export function FieldWorkPage() {
  const [range, setRange] = useState({ from: day(-30), to: day(0) });
  const [person, setPerson] = useState(EVERYBODY);
  const [open, setOpen] = useState<FieldCheckin | null>(null);

  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['field-work', range.from, range.to, person],
    queryFn: () => fieldWorkApi.list({ ...range, userId: person === EVERYBODY ? undefined : person }),
    placeholderData: keepPreviousData,
  });
  const everybody = !!data?.everybody;
  const { data: users } = useQuery({
    queryKey: ['users'],
    queryFn: () => orgApi.listUsers(),
    enabled: everybody,
  });
  const people = useMemo(() => ((users?.data ?? []) as { id: string; name: string }[]).slice().sort(byName), [users]);
  const personOptions = usePersonOptions(people, { extra: [{ value: EVERYBODY, label: 'Everybody' }] });

  const byDate = new Map<string, FieldCheckin[]>();
  for (const c of data?.checkins ?? []) byDate.set(c.onDate, [...(byDate.get(c.onDate) ?? []), c]);

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
          <h1 className="text-xl font-bold">Field work</h1>
          <p className="text-sm text-muted-foreground">
            {everybody
              ? 'Everybody’s check-ins from the field, with the photo and where it was taken.'
              : 'Your check-ins from the field, with the photo and where it was taken.'}
          </p>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
          <div className="grid grid-cols-2 gap-2">
            <div className="min-w-0 space-y-1">
              <Label htmlFor="fieldFrom" className="text-xs">From</Label>
              <Input id="fieldFrom" type="date" className="h-9" value={range.from} max={range.to} onChange={(e) => setRange({ ...range, from: e.target.value })} />
            </div>
            <div className="min-w-0 space-y-1">
              <Label htmlFor="fieldTo" className="text-xs">To</Label>
              <Input id="fieldTo" type="date" className="h-9" value={range.to} min={range.from} onChange={(e) => setRange({ ...range, to: e.target.value })} />
            </div>
          </div>
          {everybody && (
            <div className="w-full sm:ml-auto sm:w-56">
              <SearchSelect
                value={person}
                onChange={setPerson}
                options={personOptions}
                aria-label="Show check-ins of"
                searchPlaceholder="Search people"
                className="h-9"
              />
            </div>
          )}
        </div>

        {isPending ? (
          <LoadingState label="Loading field work" />
        ) : !data ? (
          <ErrorState error={error} fallback="Could not load field work." onRetry={() => refetch()} />
        ) : byDate.size === 0 ? (
          <EmptyState title="No check-ins in these dates" hint="Check in from the field on Calls, when you are working away." />
        ) : (
          [...byDate.entries()].map(([date, list]) => (
            <section key={date} aria-label={`Field work on ${date}`} className="space-y-2">
              <h2 className="text-sm font-semibold">{dayTitle(date)}</h2>
              <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {list.map((c) => (
                  <li key={c.id} className="overflow-hidden rounded-lg border border-border bg-card" data-testid="field-checkin">
                    <button
                      type="button"
                      className="block w-full bg-black"
                      onClick={() => setOpen(c)}
                      aria-label={`Open the photo${everybody && c.name ? ` from ${c.name}` : ''} at ${formatTime(c.takenAt)}`}
                    >
                      <img
                        src={fieldWorkApi.photoUrl(c.id)}
                        alt=""
                        loading="lazy"
                        // Whole, not cropped: the time and place are printed along its foot.
                        className="aspect-[4/3] w-full object-contain"
                      />
                    </button>
                    <div className="space-y-0.5 p-2 text-sm">
                      {everybody && c.name && <p className="font-medium">{c.name}</p>}
                      <p className="tabular-nums text-muted-foreground">{formatTime(c.takenAt)}</p>
                      {c.note && <p className="break-anywhere">{c.note}</p>}
                      {c.address && <p className="break-anywhere text-xs">{c.address}</p>}
                      <a
                        href={mapLink(c.latitude, c.longitude)}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground"
                      >
                        <MapPin className="h-3 w-3" aria-hidden="true" />
                        {coordinates(c.latitude, c.longitude)}, within {Math.round(c.accuracyM)} m
                      </a>
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          ))
        )}
      </div>

      <Dialog open={!!open} onOpenChange={(o) => !o && setOpen(null)}>
        {open && (
          <DialogContent className="sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>{open.name ? `${open.name}, ` : ''}field work</DialogTitle>
              <DialogDescription>
                {dayTitle(open.onDate)}, {formatTime(open.takenAt)}. {open.address ? `${open.address}. ` : ''}
                {coordinates(open.latitude, open.longitude)}, within {Math.round(open.accuracyM)} m.
              </DialogDescription>
            </DialogHeader>
            <img src={fieldWorkApi.photoUrl(open.id)} alt="Field work photo" className="max-h-[65vh] w-full rounded-md bg-muted object-contain" />
            <a
              href={mapLink(open.latitude, open.longitude)}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-sm underline underline-offset-2"
            >
              <MapPin className="h-4 w-4" aria-hidden="true" />
              Open the place on a map
            </a>
          </DialogContent>
        )}
      </Dialog>
    </div>
  );
}
