import { forwardRef, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type UIEvent } from 'react';
import { Search } from 'lucide-react';
import { indexBy, search, type Facts } from '@/shared/utils/peopleSearch';
import { Input, type InputProps } from '@/shared/components/ui/input';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { cn } from '@/shared/utils/cn';

/** How many rows are drawn at first, and how many more each time the list nears its end. */
export const SHOW_STEP = 100;

/**
 * Whether a search box may take the focus by itself. On a phone that would
 * throw the keyboard up over the list the person came to look at.
 */
export const useMayFocusSearch = () => useMediaQuery('(min-width: 768px)');

/** The search box every people and team list shares. Escape clears it first. */
export const SearchInput = forwardRef<HTMLInputElement, InputProps>(({ className, ...props }, ref) => (
  <div className="relative">
    <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
    <Input ref={ref} type="text" autoComplete="off" spellCheck={false} data-escape-clears="" className={cn('pl-9', className)} {...props} />
  </div>
));
SearchInput.displayName = 'SearchInput';

/** A list this long, or longer, to look through gets a search box over it. */
export const SEARCH_FROM = 6;

/**
 * Looking through a list of people or teams that is only to be read, such as
 * who is in a channel: the same search, best matches first, and Escape clears.
 */
export function useBrowseSearch<T>(items: T[], facts: (item: T) => Facts) {
  const [query, setQuery] = useState('');
  const index = useMemo(() => indexBy(items, facts), [items, facts]);
  const shown = useMemo(() => search(index, query), [index, query]);
  const box = (label: string, placeholder = 'Search by name, email, role or team') =>
    items.length >= SEARCH_FROM ? (
      <SearchInput
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && query) setQuery('');
        }}
        placeholder={placeholder}
        aria-label={label}
        className="h-9"
      />
    ) : null;
  const none = query.trim() !== '' && shown.length === 0;
  return { query, setQuery, shown, box, none };
}

/** What a search turned up nothing for, said plainly. */
export function NoMatch({ query, noun = 'No one' }: { query: string; noun?: string }) {
  return (
    <p className="px-2 py-3 text-center text-sm text-muted-foreground" role="status">
      {noun} matches “{query.trim()}”.
    </p>
  );
}

/**
 * The keys of a search box over a list: arrows move through what it shows,
 * Enter picks the one highlighted, or the only one there is, and Escape clears
 * what was typed. Rows are drawn a hundred at a time as the list is scrolled,
 * so an organization of thousands opens as quickly as one of ten.
 */
export function useListKeys<T>({
  shown,
  query,
  setQuery,
  onPick,
  clearOnPick,
}: {
  shown: T[];
  query: string;
  setQuery: (q: string) => void;
  onPick: (item: T) => void;
  /** Empties the box after a pick, ready for the next name. */
  clearOnPick?: boolean;
}) {
  const [active, setActive] = useState(-1);
  const [limit, setLimit] = useState(SHOW_STEP);
  const baseId = useId();
  const listId = `${baseId}-list`;
  const optionId = (i: number) => `${baseId}-o${i}`;
  const listRef = useRef<HTMLUListElement>(null);

  // A new query is a new list: nothing highlighted, back to the first rows.
  useEffect(() => {
    setActive(-1);
    setLimit(SHOW_STEP);
  }, [query]);

  const current = active < shown.length ? active : -1;

  useEffect(() => {
    if (current < 0) return;
    document.getElementById(optionId(current))?.scrollIntoView({ block: 'nearest' });
    // optionId only changes with baseId, which never does.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current]);

  const move = (to: number) => {
    if (shown.length === 0) return;
    const next = Math.max(0, Math.min(to, shown.length - 1));
    if (next >= limit) setLimit(next + SHOW_STEP);
    setActive(next);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        move(current + 1);
        break;
      case 'ArrowUp':
        e.preventDefault();
        move(current <= 0 ? 0 : current - 1);
        break;
      case 'Enter': {
        // Never the dialog's submit: Enter here is for the list.
        e.preventDefault();
        const pick = current >= 0 ? shown[current] : shown.length === 1 ? shown[0] : undefined;
        if (pick === undefined) return;
        onPick(pick);
        if (clearOnPick) setQuery('');
        break;
      }
      case 'Escape':
        if (query) {
          e.preventDefault();
          e.stopPropagation();
          setQuery('');
        }
        break;
    }
  };

  const onScroll = (e: UIEvent<HTMLElement>) => {
    const el = e.currentTarget;
    if (limit < shown.length && el.scrollTop + el.clientHeight >= el.scrollHeight - 120) setLimit((l) => l + SHOW_STEP);
  };

  return {
    active: current,
    setActive,
    onKeyDown,
    onScroll,
    limit,
    listId,
    listRef,
    optionId,
    /** For the search box: which row the arrows are on, for a screen reader. */
    inputProps: {
      'aria-controls': listId,
      'aria-activedescendant': current >= 0 ? optionId(current) : undefined,
    },
  };
}
