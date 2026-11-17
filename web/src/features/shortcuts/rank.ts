/**
 * What matches a search typed into the palette, best first: a name that
 * starts with it, then one with a word that does, then one that merely
 * contains it. Among equals the original order stands.
 */
export function rank<T>(items: T[], term: string, fields: (item: T) => (string | null | undefined)[]): T[] {
  const needle = term.trim().toLowerCase();
  if (!needle) return items;
  const scored: { item: T; score: number; index: number }[] = [];
  items.forEach((item, index) => {
    let best = Infinity;
    for (const field of fields(item)) {
      const text = (field ?? '').toLowerCase();
      if (!text) continue;
      if (text.startsWith(needle)) best = Math.min(best, 0);
      else if (text.split(/[\s\-_.@/]+/).some((word) => word.startsWith(needle))) best = Math.min(best, 1);
      else if (text.includes(needle)) best = Math.min(best, 2);
    }
    if (best < Infinity) scored.push({ item, score: best, index });
  });
  return scored.sort((a, b) => a.score - b.score || a.index - b.index).map((s) => s.item);
}
