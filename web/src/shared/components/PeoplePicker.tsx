import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Check, UsersRound, X } from 'lucide-react';
import { UserAvatar } from '@/shared/components/UserAvatar';
import { usePeopleDirectory, type Directory } from '@/shared/components/search/directory';
import { NoMatch, SearchInput, useListKeys, useMayFocusSearch } from '@/shared/components/search/listKeys';
import { byName, indexBy, personFacts, search, teamFacts, type Facts } from '@/shared/utils/peopleSearch';
import { cn } from '@/shared/utils/cn';

export interface Pickable {
  id: string;
  name: string;
  email?: string;
  avatarUrl?: string | null;
  jobTitle?: string;
}

interface MultiPickerProps<T> {
  items: T[];
  idOf: (item: T) => string;
  nameOf: (item: T) => string;
  facts: (item: T) => Facts;
  /** What a row shows after its tick box. */
  row: (item: T) => ReactNode;
  selected: string[];
  onChange: (ids: string[]) => void;
  /** What to say when there is nothing left to choose. */
  empty: string;
  listClassName?: string;
  /** Puts the cursor in the search box on opening, on a computer only. */
  autoFocus?: boolean;
  placeholder: string;
  searchLabel: string;
  listLabel: string;
  /** "No one" for people, "No team" for teams. */
  nobody: string;
  /** The line under the list, for how many are chosen. */
  footer?: (count: number) => string;
  /** Keeps the list folded away until somebody types, once something is chosen. */
  listOnlyWhileSearching?: boolean;
  inputId?: string;
}

/**
 * A list to tick any number from, with a search over it. Whoever is ticked
 * stays in view above the list, even while a search hides them below.
 */
function MultiPicker<T>({
  items,
  idOf,
  nameOf,
  facts,
  row,
  selected,
  onChange,
  empty,
  listClassName = 'max-h-64',
  autoFocus,
  placeholder,
  searchLabel,
  listLabel,
  nobody,
  footer,
  listOnlyWhileSearching,
  inputId,
}: MultiPickerProps<T>) {
  const [query, setQuery] = useState('');
  const index = useMemo(() => indexBy(items, facts), [items, facts]);
  const shown = useMemo(() => search(index, query), [index, query]);
  const chosen = useMemo(() => items.filter((p) => selected.includes(idOf(p))), [items, selected, idOf]);

  const toggle = (id: string) =>
    onChange(selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id]);

  const keys = useListKeys({ shown, query, setQuery, onPick: (item: T) => toggle(idOf(item)), clearOnPick: true });

  const inputRef = useRef<HTMLInputElement>(null);
  const mayFocus = useMayFocusSearch();
  // Once, as soon as there is a box to focus: the list may still be loading
  // when the dialog opens. Turning a phone round later grabs nothing.
  const focused = useRef(false);
  const hasBox = items.length > 0;
  useEffect(() => {
    if (!autoFocus || !mayFocus || !hasBox || focused.current) return;
    focused.current = true;
    inputRef.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasBox]);

  if (items.length === 0) return <p className="text-sm text-muted-foreground">{empty}</p>;

  const showList = !listOnlyWhileSearching || query.trim() !== '' || selected.length === 0;

  return (
    <div className="space-y-2">
      <SearchInput
        ref={inputRef}
        id={inputId}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={placeholder}
        aria-label={searchLabel}
        onKeyDown={keys.onKeyDown}
        {...keys.inputProps}
      />

      {chosen.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label="Chosen">
          {chosen.map((p) => (
            <li
              key={idOf(p)}
              className="flex min-w-0 max-w-full items-center gap-1 rounded-full bg-primary/10 py-0.5 pl-2.5 pr-1 text-xs font-medium text-primary"
            >
              <span className="truncate">{nameOf(p)}</span>
              <button
                type="button"
                onClick={() => toggle(idOf(p))}
                aria-label={`Don't add ${nameOf(p)}`}
                className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full hover:bg-primary/20"
              >
                <X className="h-3 w-3" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      )}

      {showList && (
        <ul
          id={keys.listId}
          ref={keys.listRef}
          onScroll={keys.onScroll}
          className={cn(listClassName, 'overflow-y-auto overscroll-contain rounded-md border border-border p-1')}
          aria-label={listLabel}
        >
          {shown.length === 0 && (
            <li>
              <NoMatch query={query} noun={nobody} />
            </li>
          )}
          {shown.slice(0, keys.limit).map((p, i) => {
            const id = idOf(p);
            const on = selected.includes(id);
            return (
              <li key={id}>
                <label
                  onMouseMove={() => keys.active !== i && keys.setActive(i)}
                  className={cn(
                    'flex cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 hover:bg-accent',
                    on && 'bg-primary/5',
                    keys.active === i && 'bg-accent ring-1 ring-inset ring-ring/40'
                  )}
                >
                  <span className="relative flex h-4 w-4 shrink-0">
                    <input
                      id={keys.optionId(i)}
                      type="checkbox"
                      checked={on}
                      onChange={() => toggle(id)}
                      className="peer h-4 w-4 cursor-pointer appearance-none rounded border border-input bg-background checked:border-primary checked:bg-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1"
                    />
                    <Check
                      className="pointer-events-none absolute inset-0 m-auto h-3 w-3 text-primary-foreground opacity-0 peer-checked:opacity-100"
                      strokeWidth={3}
                      aria-hidden="true"
                    />
                  </span>
                  {row(p)}
                </label>
              </li>
            );
          })}
          {shown.length > keys.limit && (
            <li className="px-2 py-1.5 text-center text-xs text-muted-foreground">
              {shown.length - keys.limit} more below. Keep scrolling, or type to narrow it down.
            </li>
          )}
        </ul>
      )}
      {footer && (
        <p className="text-xs text-muted-foreground" aria-live="polite">
          {footer(selected.length)}
        </p>
      )}
    </div>
  );
}

/** Somebody's picture, name, and what else the search can find them by. */
export function PersonRow({ person, about, note }: { person: Pickable; about?: { email?: string; jobTitle?: string }; note?: ReactNode }) {
  const jobTitle = person.jobTitle || about?.jobTitle;
  const email = person.email || about?.email;
  return (
    <>
      <UserAvatar name={person.name} src={person.avatarUrl} className="h-7 w-7 shrink-0" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{person.name}</span>
        {(jobTitle || email) && (
          <span className="block truncate text-xs text-muted-foreground">{[jobTitle, email].filter(Boolean).join(' · ')}</span>
        )}
        {note}
      </span>
    </>
  );
}

/** What the app's one search rule may find a person by, filled in from the member list. */
export function usePersonFacts<T extends Pickable>(directory: Directory) {
  return useMemo(
    () => (p: T) => {
      const about = directory.about(p.id);
      return personFacts({ name: p.name, email: p.email || about?.email, jobTitle: p.jobTitle || about?.jobTitle, teams: about?.teams });
    },
    [directory]
  );
}

/**
 * Choosing people to add, any number at once.
 *
 * Adding a team of eight one dropdown at a time meant opening the same dialog
 * eight times. This is a list to tick instead, with a search for a long one:
 * by name, email, company role or team, and Enter ticks the only match.
 */
export function PeoplePicker({
  people,
  selected,
  onChange,
  empty = 'Everybody is already here.',
  listClassName = 'max-h-64',
  autoFocus,
  note,
  listOnlyWhileSearching,
  inputId,
}: {
  people: Pickable[];
  selected: string[];
  onChange: (ids: string[]) => void;
  /** What to say when there is nobody left to choose. */
  empty?: string;
  /** The list's height, for a dialog that has other things in it too. */
  listClassName?: string;
  /** Puts the cursor in the search on opening, on a computer only. */
  autoFocus?: boolean;
  /** Something to say at the end of a person's row, such as being on leave. */
  note?: (person: Pickable) => ReactNode;
  listOnlyWhileSearching?: boolean;
  inputId?: string;
}) {
  const sorted = useMemo(() => [...people].sort(byName), [people]);
  const directory = usePeopleDirectory();
  const facts = usePersonFacts<Pickable>(directory);
  return (
    <MultiPicker<Pickable>
      items={sorted}
      idOf={idOf}
      nameOf={nameOf}
      facts={facts}
      row={(p) => <PersonRow person={p} about={directory.about(p.id)} note={note?.(p)} />}
      selected={selected}
      onChange={onChange}
      empty={empty}
      listClassName={listClassName}
      autoFocus={autoFocus}
      placeholder="Search by name, email, role or team"
      searchLabel="Find people"
      listLabel="People"
      nobody="No one"
      footer={(n) => (n === 0 ? 'Tick everyone you want to add.' : `${n} chosen.`)}
      listOnlyWhileSearching={listOnlyWhileSearching}
      inputId={inputId}
    />
  );
}

export interface PickableTeam {
  id: string;
  name: string;
}

/** Choosing teams, any number at once, found by their name or by who is on them. */
export function TeamPicker({
  teams,
  selected,
  onChange,
  empty = 'There are no teams to choose.',
  listClassName = 'max-h-40',
}: {
  teams: PickableTeam[];
  selected: string[];
  onChange: (ids: string[]) => void;
  empty?: string;
  listClassName?: string;
}) {
  const directory = usePeopleDirectory();
  const sorted = useMemo(() => [...teams].sort(byName), [teams]);
  const facts = useMemo(
    () => (t: PickableTeam) => teamFacts({ name: t.name, members: directory.membersOf(t.name) }),
    [directory]
  );
  return (
    <MultiPicker<PickableTeam>
      items={sorted}
      idOf={idOf}
      nameOf={nameOf}
      facts={facts}
      row={(t) => <TeamRow name={t.name} members={directory.membersOf(t.name)} />}
      selected={selected}
      onChange={onChange}
      empty={empty}
      listClassName={listClassName}
      placeholder="Search teams, or who is on them"
      searchLabel="Find teams"
      listLabel="Teams"
      nobody="No team"
    />
  );
}

function TeamRow({ name, members }: { name: string; members: string[] }) {
  return (
    <>
      <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <UsersRound className="h-3.5 w-3.5" aria-hidden="true" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{name}</span>
        {members.length > 0 && (
          <span className="block truncate text-xs text-muted-foreground">
            {members.length === 1 ? '1 person' : `${members.length} people`}: {[...members].sort().join(', ')}
          </span>
        )}
      </span>
    </>
  );
}

const idOf = (x: { id: string }) => x.id;
const nameOf = (x: { name: string }) => x.name;

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
