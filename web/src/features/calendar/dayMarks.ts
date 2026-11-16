import { format } from 'date-fns';

/** Leave or a holiday on somebody's calendar (internal/calendar DayMarks). */
export interface DayMark {
  kind: 'leave' | 'holiday';
  /** First and last day, as plain dates. `to` is missing for leave with no end yet. */
  from: string;
  to?: string;
  part?: 'full' | 'morning' | 'afternoon';
  name?: string;
}

/** What a mark says on the day it covers. */
export function markLabel(mark: DayMark, whose?: string): string {
  if (mark.kind === 'holiday') return `Holiday: ${mark.name ?? ''}`.trim();
  const who = whose ? `${whose} on leave` : 'On leave';
  if (mark.part === 'morning') return `${who}, morning`;
  if (mark.part === 'afternoon') return `${who}, afternoon`;
  return who;
}

/** The marks that cover a day, holidays first. Dates compare as text, so no zone moves them. */
export function marksOn(marks: DayMark[], day: Date): DayMark[] {
  const date = format(day, 'yyyy-MM-dd');
  return marks
    .filter((m) => m.from <= date && (!m.to || m.to >= date))
    .sort((a, b) => (a.kind === b.kind ? 0 : a.kind === 'holiday' ? -1 : 1));
}
