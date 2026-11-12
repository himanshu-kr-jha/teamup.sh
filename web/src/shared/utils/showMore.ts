/**
 * The part of a long list worth showing at first: the first few, or for a list
 * whose newest is at the end (comments), the last few. Everything once asked for.
 */
export function visiblePart<T>(items: T[], first: number, expanded: boolean, keep: 'start' | 'end' = 'start'): { visible: T[]; hidden: number } {
  if (expanded || items.length <= first) return { visible: items, hidden: 0 };
  return {
    visible: keep === 'start' ? items.slice(0, first) : items.slice(items.length - first),
    hidden: items.length - first,
  };
}

/** What the button says: how many more, or that it can fold them away again. */
export function showMoreText(hidden: number, expanded: boolean, noun: [string, string], earlier = false): string {
  if (expanded) return 'Show less';
  return `Show ${hidden} ${earlier ? 'earlier' : 'more'} ${hidden === 1 ? noun[0] : noun[1]}`;
}
