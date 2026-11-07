import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { bucketSections } from './logic';
import { BucketList, EmptyState, ErrorState, LoadingState, StatTiles, TeamFilter, ViewFrame } from './parts';
import { useDashboard } from './useDashboard';
import { useTabParams } from './useTabParams';

export function ByPersonTab() {
  const { team, person: personId, update } = useTabParams();
  const { data, error, refetch } = useDashboard(team);

  if (!data) {
    return (
      <ViewFrame>
        {error ? (
          <ErrorState error={error} fallback="Could not load your people." onRetry={() => refetch()} />
        ) : (
          <LoadingState label="Loading your people" />
        )}
      </ViewFrame>
    );
  }

  const people = [...data.people].sort((a, b) => a.name.localeCompare(b.name));
  // An id left in the address after the filter changed is simply nobody.
  const person = people.find((p) => p.userId === personId);
  const sections = person ? bucketSections(person.tasks) : [];

  return (
    <ViewFrame>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <Select value={person?.userId ?? ''} onValueChange={(v) => update({ person: v })} disabled={people.length === 0}>
          <SelectTrigger className="h-9 w-full sm:w-64" aria-label="Person">
            <SelectValue placeholder="Pick a person" />
          </SelectTrigger>
          <SelectContent>
            {people.map((p) => (
              <SelectItem key={p.userId} value={p.userId}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <TeamFilter teams={data.teams} />
      </div>

      {people.length === 0 ? (
        <EmptyState title="Nobody here yet" hint="People on the teams you manage will show up here." />
      ) : !person ? (
        <EmptyState title="Pick a person" hint="You will see only their tasks, by status." />
      ) : (
        <>
          <StatTiles counts={person.counts} />
          {sections.length > 0 ? (
            <BucketList sections={sections} />
          ) : (
            <EmptyState title={`${person.name} has no tasks`} />
          )}
        </>
      )}
    </ViewFrame>
  );
}
