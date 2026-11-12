/** Colours a new label is given, readable as a dot on light and dark alike. */
export const LABEL_COLORS = ['#6366f1', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444', '#ec4899', '#8b5cf6', '#14b8a6', '#f97316', '#64748b'];

/** The same name always gets the same colour, so a label looks alike wherever it is made. */
export function colorFor(name: string): string {
  let h = 0;
  for (const ch of name.trim().toLowerCase()) h = (h * 31 + ch.codePointAt(0)!) >>> 0;
  return LABEL_COLORS[h % LABEL_COLORS.length];
}

/** The longest label name the server takes. */
export const MAX_LABEL = 50;

/** A label the organization already has by this name, whatever its case. */
export function existingLabel<T extends { name: string }>(all: T[], name: string): T | undefined {
  const want = name.trim().toLowerCase();
  return all.find((l) => l.name.toLowerCase() === want);
}
