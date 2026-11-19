import { Fragment, useMemo, useState, type ReactNode } from 'react';
import { Check, ChevronDown } from 'lucide-react';
import { Popover, PopoverContent, PopoverTrigger } from '@/shared/components/ui/popover';
import { usePeopleDirectory } from '@/shared/components/search/directory';
import { NoMatch, SearchInput, useListKeys } from '@/shared/components/search/listKeys';
import { indexBy, personFacts, search, teamFacts, type Facts } from '@/shared/utils/peopleSearch';
import { cn } from '@/shared/utils/cn';

export interface SelectOption {
  /** Unique in the list; the same person under two teams has two. */
  key: string;
  value: string;
  label: string;
  facts: Facts;
  /** A heading the option is listed under while nothing is typed. */
  group?: string;
}

/**
 * Choosing one from a long list: a button that opens the list with a search
 * box over it. It is the dropdown it replaces, and finds people and teams by
 * the app's one search rule; arrows move, Enter picks the highlighted one or
 * the only match, and Escape clears the search before it closes the list.
 */
export function SearchSelect({
  value,
  onChange,
  options,
  placeholder = 'Choose',
  display,
  searchPlaceholder,
  nobody = 'No one',
  id,
  'aria-label': ariaLabel,
  className,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  placeholder?: string;
  /** What the button says, when the chosen value is not one of the options. */
  display?: ReactNode;
  searchPlaceholder: string;
  nobody?: string;
  id?: string;
  'aria-label'?: string;
  className?: string;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const index = useMemo(() => indexBy(options, (o) => o.facts), [options]);
  const searching = query.trim() !== '';
  const shown = useMemo(() => {
    const found = search(index, query);
    if (!searching) return found;
    // A person under two teams is one match, not two.
    const seen = new Set<string>();
    return found.filter((o) => !seen.has(o.value) && !!seen.add(o.value));
  }, [index, query, searching]);
  const chosen = options.find((o) => o.value === value);

  const pick = (o: SelectOption) => {
    onChange(o.value);
    setOpen(false);
  };
  const keys = useListKeys({ shown, query, setQuery, onPick: pick });

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setQuery('');
      }}
      // Modal, so it scrolls and takes the focus properly inside a dialog too.
      modal
    >
      <PopoverTrigger
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={ariaLabel}
        disabled={disabled}
        className={cn(
          'flex h-10 w-full items-center justify-between gap-2 rounded-md border border-input bg-background px-3 py-2 text-left text-sm ring-offset-background focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50',
          className
        )}
      >
        <span className={cn('min-w-0 truncate', !chosen && !display && 'text-muted-foreground')}>
          {display ?? chosen?.label ?? placeholder}
        </span>
        <ChevronDown className="h-4 w-4 shrink-0 opacity-50" aria-hidden="true" />
      </PopoverTrigger>
      <PopoverContent
        className="flex w-[var(--radix-popover-trigger-width)] min-w-[16rem] max-w-[calc(100vw-2rem)] flex-col gap-1"
        collisionPadding={8}
        onOpenAutoFocus={(e) => {
          // The search box, not the first option, and only where a keyboard is to hand.
          if (!window.matchMedia('(min-width: 768px)').matches) {
            e.preventDefault();
            (e.currentTarget as HTMLElement).focus({ preventScroll: true });
          }
        }}
      >
        <SearchInput
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={searchPlaceholder}
          aria-label={searchPlaceholder}
          role="combobox"
          aria-expanded
          aria-autocomplete="list"
          onKeyDown={keys.onKeyDown}
          className="h-9"
          {...keys.inputProps}
        />
        <ul
          id={keys.listId}
          role="listbox"
          ref={keys.listRef}
          onScroll={keys.onScroll}
          className="max-h-[min(18rem,45vh)] overflow-y-auto overscroll-contain"
        >
          {shown.length === 0 && (
            <li role="presentation">
              <NoMatch query={query} noun={nobody} />
            </li>
          )}
          {groupRuns(shown.slice(0, keys.limit), !searching).map((run, r) => {
            const rows = run.items.map(({ option: o, at }) => (
              <li
                key={o.key}
                id={keys.optionId(at)}
                role="option"
                aria-selected={o.value === value}
                onClick={() => pick(o)}
                onMouseMove={() => keys.active !== at && keys.setActive(at)}
                className={cn(
                  'relative flex cursor-pointer select-none items-center rounded-sm py-1.5 pl-8 pr-2 text-sm',
                  keys.active === at && 'bg-accent text-accent-foreground'
                )}
              >
                {o.value === value && <Check className="absolute left-2 h-4 w-4" aria-hidden="true" />}
                <span className="min-w-0 truncate">{o.label}</span>
              </li>
            ));
            return run.group ? (
              <li key={`g${r}`} role="presentation">
                <ul role="group" aria-labelledby={`${keys.listId}-g${r}`}>
                  {r > 0 && <li role="presentation" className="-mx-1 my-1 h-px bg-muted" />}
                  <li role="presentation" id={`${keys.listId}-g${r}`} className="px-2 pb-1 pt-2 text-xs font-medium text-muted-foreground">
                    {run.group}
                  </li>
                  {rows}
                </ul>
              </li>
            ) : (
              <Fragment key={`g${r}`}>{rows}</Fragment>
            );
          })}
          {shown.length > keys.limit && (
            <li role="presentation" className="px-2 py-1.5 text-center text-xs text-muted-foreground">
              {shown.length - keys.limit} more below. Keep scrolling, or type to narrow it down.
            </li>
          )}
        </ul>
      </PopoverContent>
    </Popover>
  );
}

/** Consecutive options under the same heading, keeping each one's place in the list. */
function groupRuns(options: SelectOption[], grouped: boolean) {
  const runs: Array<{ group?: string; items: Array<{ option: SelectOption; at: number }> }> = [];
  options.forEach((option, at) => {
    const group = grouped ? option.group : undefined;
    const last = runs[runs.length - 1];
    if (last && last.group === group) last.items.push({ option, at });
    else runs.push({ group, items: [{ option, at }] });
  });
  return runs;
}

export interface SelectablePerson {
  id: string;
  name: string;
  email?: string;
  jobTitle?: string;
}

/** Options for a list of people, findable by name, email, company role and team. */
export function usePersonOptions(
  people: SelectablePerson[],
  opts: { extra?: Array<{ value: string; label: string }>; label?: (p: SelectablePerson, i: number) => string; group?: (p: SelectablePerson) => string | undefined } = {}
): SelectOption[] {
  const directory = usePeopleDirectory();
  const { extra = [], label, group } = opts;
  const extraKey = extra.map((e) => `${e.value}=${e.label}`).join('|');
  return useMemo(
    () => [
      ...extra.map((e) => ({ key: `x:${e.value}`, value: e.value, label: e.label, facts: { primary: [e.label] } })),
      ...people.map((p, i) => {
        const about = directory.about(p.id);
        const g = group?.(p);
        return {
          key: g ? `${g}:${p.id}` : p.id,
          value: p.id,
          label: label ? label(p, i) : p.name,
          group: g,
          facts: personFacts({ name: p.name, email: p.email || about?.email, jobTitle: p.jobTitle || about?.jobTitle, teams: about?.teams }),
        };
      }),
    ],
    // `extra`, `label` and `group` are new functions and arrays each render; what
    // they give is decided by `people` and `extraKey`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [people, extraKey, directory]
  );
}

/** Options for a list of teams, findable by name and by who is on them. */
export function useTeamOptions(teams: Array<{ id: string; name: string }>, extra: Array<{ value: string; label: string }> = []): SelectOption[] {
  const directory = usePeopleDirectory();
  const extraKey = extra.map((e) => `${e.value}=${e.label}`).join('|');
  return useMemo(
    () => [
      ...extra.map((e) => ({ key: `x:${e.value}`, value: e.value, label: e.label, facts: { primary: [e.label] } })),
      ...teams.map((t) => ({ key: t.id, value: t.id, label: t.name, facts: teamFacts({ name: t.name, members: directory.membersOf(t.name) }) })),
    ],
    // `extra` is a new array each render; `extraKey` is what it says.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [teams, extraKey, directory]
  );
}
