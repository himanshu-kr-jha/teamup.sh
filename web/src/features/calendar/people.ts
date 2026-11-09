/** Somebody whose calendar you may open besides your own (decision 64). */
export interface CalendarPerson {
  id: string;
  name: string;
}

/** One team of them, or for an administrator everybody else, as the server groups them. */
export interface CalendarGroup {
  teamId?: string;
  name: string;
  people: CalendarPerson[];
}

export interface Whose {
  /** The person whose calendar is read. */
  id: string;
  own: boolean;
  /** Their name, when the picker or the member list knows it. */
  name: string | null;
}

/**
 * Whose calendar the page shows, from `?person=` in the address. No person, or
 * yourself, is your own; anybody else is read-only, and is named from the
 * picker's list first and the member list after, because an administrator may
 * follow a link to somebody the picker never listed.
 */
export function whoseCalendar(
  person: string | null,
  me: string | undefined,
  groups: CalendarGroup[],
  members: { id: string; name: string }[] = []
): Whose {
  if (!person || person === me) return { id: me ?? '', own: true, name: null };
  const listed = groups.flatMap((g) => g.people).find((p) => p.id === person);
  const member = members.find((m) => m.id === person);
  return { id: person, own: false, name: listed?.name ?? member?.name ?? null };
}

/** "Alice Archer's", or "Their" for somebody not known by name. */
export function possessive(name: string | null): string {
  if (!name) return 'Their';
  return name.endsWith('s') ? `${name}'` : `${name}'s`;
}
