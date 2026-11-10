import { useMemo, useState } from 'react';
import { Check, Search, X } from 'lucide-react';
import { Input } from '@/shared/components/ui/input';
import { UserAvatar } from '@/shared/components/UserAvatar';
import { cn } from '@/shared/utils/cn';

export interface Pickable {
  id: string;
  name: string;
  email?: string;
  avatarUrl?: string | null;
}

/**
 * Choosing people to add, any number at once.
 *
 * Adding a team of eight one dropdown at a time meant opening the same dialog
 * eight times. This is a list to tick instead, with a search for a long one;
 * ticking one person is the same single add it always was.
 */
export function PeoplePicker({
  people,
  selected,
  onChange,
  empty = 'Everybody is already here.',
  listClassName = 'max-h-64',
}: {
  people: Pickable[];
  selected: string[];
  onChange: (ids: string[]) => void;
  /** What to say when there is nobody left to choose. */
  empty?: string;
  /** The list's height, for a dialog that has other things in it too. */
  listClassName?: string;
}) {
  const [query, setQuery] = useState('');
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const sorted = [...people].sort((a, b) => a.name.localeCompare(b.name));
    if (!q) return sorted;
    return sorted.filter((p) => p.name.toLowerCase().includes(q) || p.email?.toLowerCase().includes(q));
  }, [people, query]);
  const chosen = people.filter((p) => selected.includes(p.id));

  const toggle = (id: string) =>
    onChange(selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id]);

  if (people.length === 0) return <p className="text-sm text-muted-foreground">{empty}</p>;

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search by name or email"
          aria-label="Find people"
          className="pl-9"
          // Enter in here would submit the dialog with whoever happened to be ticked.
          onKeyDown={(e) => {
            if (e.key === 'Enter') e.preventDefault();
          }}
        />
      </div>

      {chosen.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label="Chosen">
          {chosen.map((p) => (
            <li key={p.id} className="flex items-center gap-1 rounded-full bg-primary/10 py-0.5 pl-2.5 pr-1 text-xs font-medium text-primary">
              {p.name}
              <button
                type="button"
                onClick={() => toggle(p.id)}
                aria-label={`Don't add ${p.name}`}
                className="flex h-4 w-4 items-center justify-center rounded-full hover:bg-primary/20"
              >
                <X className="h-3 w-3" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      )}

      <ul className={cn(listClassName, 'overflow-y-auto rounded-md border border-border p-1')} aria-label="People">
        {shown.length === 0 && (
          <li className="px-2 py-3 text-center text-sm text-muted-foreground">Nobody matches “{query.trim()}”.</li>
        )}
        {shown.map((p) => {
          const on = selected.includes(p.id);
          return (
            <li key={p.id}>
              <label
                className={cn(
                  'flex cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 hover:bg-accent',
                  on && 'bg-primary/5'
                )}
              >
                <span className="relative flex h-4 w-4 shrink-0">
                  <input
                    type="checkbox"
                    checked={on}
                    onChange={() => toggle(p.id)}
                    className="peer h-4 w-4 cursor-pointer appearance-none rounded border border-input bg-background checked:border-primary checked:bg-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1"
                  />
                  <Check
                    className="pointer-events-none absolute inset-0 m-auto h-3 w-3 text-primary-foreground opacity-0 peer-checked:opacity-100"
                    strokeWidth={3}
                    aria-hidden="true"
                  />
                </span>
                <UserAvatar name={p.name} src={p.avatarUrl} className="h-7 w-7" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{p.name}</span>
                  {p.email && <span className="block truncate text-xs text-muted-foreground">{p.email}</span>}
                </span>
              </label>
            </li>
          );
        })}
      </ul>
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {selected.length === 0 ? 'Tick everyone you want to add.' : `${selected.length} chosen.`}
      </p>
    </div>
  );
}

/**
 * Adds each chosen person with `add`, all together. Everyone who could be added
 * is; if anybody could not, the error names them, and `stillToAdd` is told who,
 * so the dialog stays open on just those people.
 */
export async function addEach(
  ids: string[],
  add: (id: string) => Promise<unknown>,
  nameOf: (id: string) => string,
  stillToAdd: (ids: string[]) => void
) {
  const results = await Promise.allSettled(ids.map((id) => add(id)));
  const failed = ids.filter((_, i) => results[i].status === 'rejected');
  if (failed.length === 0) return;
  stillToAdd(failed);
  const first = results.find((r) => r.status === 'rejected') as PromiseRejectedResult;
  const reason = (first.reason as any)?.response?.data?.error;
  const names = failed.map(nameOf).join(', ');
  throw new Error(`Could not add ${names}${reason ? `: ${reason}` : '.'}`);
}

/** "Add", "Add 3 people". */
export function addLabel(count: number, one = 'Add', many = (n: number) => `Add ${n} people`) {
  return count > 1 ? many(count) : one;
}
