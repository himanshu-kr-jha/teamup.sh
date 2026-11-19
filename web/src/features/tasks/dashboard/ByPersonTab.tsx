import { useMemo } from 'react';
import { SearchSelect, usePersonOptions } from '@/shared/components/SearchSelect';
import { byName } from '@/shared/utils/peopleSearch';
import { bucketSections } from './logic';
import { BucketList, EmptyState, ErrorState, LoadingState, StatTiles, TeamFilter, ViewFrame } from './parts';
import { useDashboard } from './useDashboard';
import { useTabParams } from './useTabParams';

export function ByPersonTab() {
  const { team, person: personId, update } = useTabParams();
  const { data, error, refetch } = useDashboard(team);
  const people = useMemo(() => [...(data?.people ?? [])].sort(byName), [data]);
  const options = usePersonOptions(useMemo(() => people.map((p) => ({ id: p.userId, name: p.name })), [people]));

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

  // An id left in the address after the filter changed is simply nobody.
  const person = people.find((p) => p.userId === personId);
  const sections = person ? bucketSections(person.tasks) : [];

  return (
    <ViewFrame>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <SearchSelect
          value={person?.userId ?? ''}
          onChange={(v) => update({ person: v })}
          options={options}
          disabled={people.length === 0}
          placeholder="Pick a person"
          aria-label="Person"
          searchPlaceholder="Search people"
          className="h-9 w-full sm:w-64"
        />
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
