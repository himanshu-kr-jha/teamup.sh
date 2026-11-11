import { format, isSameDay, isSameMonth } from 'date-fns';

import { cn, formatTime } from '@/shared/utils/cn';
import { itemKey, itemLabel, itemsOnDay, type CalendarItem } from './layout';
import type { DueTask } from '@/shared/api/client';
import { DueChip, tasksDueOn } from './DueTasks';

/** How many things a day lists before saying how many more. */
const SHOWN_PER_DAY = 3;

const KIND_DOT: Record<CalendarItem['kind'], string> = {
  busy: 'bg-primary',
  focus: 'bg-amber-500',
  open: 'bg-emerald-500',
  entry: 'bg-violet-500',
};

/**
 * A month at a glance: each day lists what is on it, and opens in the day view
 * when there is more than fits. On a phone a day is too narrow for names, so it
 * shows a dot for each thing instead.
 */
export function MonthGrid({
  days,
  anchor,
  items,
  now,
  onSelect,
  onOpenDay,
  dueTasks = [],
}: {
  days: Date[];
  anchor: Date;
  items: CalendarItem[];
  /** Work due each day, listed first: a deadline is what a month view is for. */
  dueTasks?: DueTask[];
  now: Date;
  onSelect: (item: CalendarItem) => void;
  onOpenDay: (day: Date) => void;
}) {
  const weeks = days.length / 7;
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-auto" data-testid="month-grid">
      <div className="grid shrink-0 grid-cols-7 border-b border-border text-center text-xs font-medium text-muted-foreground">
        {days.slice(0, 7).map((d) => (
          <div key={d.toISOString()} className="py-2">
            <span className="hidden sm:inline">{format(d, 'EEE')}</span>
            <span className="sm:hidden">{format(d, 'EEEEE')}</span>
          </div>
        ))}
      </div>
      <div
        className="grid min-h-[28rem] flex-1 grid-cols-7"
        style={{ gridTemplateRows: `repeat(${weeks}, minmax(5.5rem, 1fr))` }}
        role="grid"
        aria-label={format(anchor, 'MMMM yyyy')}
      >
        {days.map((day) => {
          const inMonth = isSameMonth(day, anchor);
          const today = isSameDay(day, now);
          const onDay = itemsOnDay(items, day);
          const due = tasksDueOn(dueTasks, day);
          const dueShown = due.slice(0, SHOWN_PER_DAY);
          const itemsShown = onDay.slice(0, SHOWN_PER_DAY - dueShown.length);
          const total = onDay.length + due.length;
          const hidden = total - dueShown.length - itemsShown.length;
          return (
            <div
              key={day.toISOString()}
              role="gridcell"
              aria-label={`${format(day, 'EEEE, MMMM d')}${total ? `, ${total} ${total === 1 ? 'thing' : 'things'}` : ''}`}
              className={cn(
                'group relative flex min-w-0 flex-col gap-0.5 border-b border-r border-border p-1 text-left',
                !inMonth && 'bg-muted/30'
              )}
              data-testid="month-day"
              data-day={format(day, 'yyyy-MM-dd')}
            >
              {/* The whole day is a way into it, under whatever it lists. */}
              <button
                type="button"
                className="absolute inset-0 hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none"
                onClick={() => onOpenDay(day)}
                aria-label={`Open ${format(day, 'EEEE, MMMM d')}`}
              />
              <span
                className={cn(
                  'pointer-events-none relative flex h-6 w-6 items-center justify-center self-start rounded-full text-xs tabular-nums',
                  today ? 'bg-primary font-semibold text-primary-foreground' : inMonth ? 'text-foreground' : 'text-muted-foreground'
                )}
              >
                {format(day, 'd')}
              </span>

              {/* Phones: one dot each. */}
              {total > 0 && (
                <span className="pointer-events-none relative flex flex-wrap gap-0.5 sm:hidden" aria-hidden="true">
                  {due.slice(0, 4).map((task) => (
                    <span key={task.id} className="h-1.5 w-1.5 rounded-[1px] bg-foreground/60" />
                  ))}
                  {onDay.slice(0, Math.max(0, 4 - due.length)).map((item, i) => (
                    <span key={itemKey(item, i)} className={cn('h-1.5 w-1.5 rounded-full', KIND_DOT[item.kind])} />
                  ))}
                </span>
              )}

              {/* Wider: what each thing is. */}
              {/* Only the entries themselves catch clicks: the space between and below
                  them is the day's, and opens it. */}
              <ul className="pointer-events-none relative hidden min-w-0 space-y-0.5 sm:block">
                {dueShown.map((task) => (
                  <li key={task.id}>
                    <DueChip task={task} now={now} compact />
                  </li>
                ))}
                {itemsShown.map((item, i) => (
                  <li key={itemKey(item, i)}>
                    <button
                      type="button"
                      onClick={() => onSelect(item)}
                      className="pointer-events-auto flex w-full min-w-0 items-center gap-1 rounded px-1 py-0.5 text-left text-xs hover:bg-accent"
                      data-testid="month-item"
                    >
                      <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', KIND_DOT[item.kind])} aria-hidden="true" />
                      {isSameDay(new Date(item.startsAt), day) && (
                        <span className="shrink-0 tabular-nums text-muted-foreground">
                          {formatTime(new Date(item.startsAt))}
                        </span>
                      )}
                      <span className="min-w-0 truncate">{itemLabel(item)}</span>
                    </button>
                  </li>
                ))}
                {hidden > 0 && (
                  <li>
                    <button
                      type="button"
                      onClick={() => onOpenDay(day)}
                      className="pointer-events-auto w-full rounded px-1 text-left text-xs font-medium text-muted-foreground hover:bg-accent hover:text-foreground"
                    >
                      +{hidden} more
                    </button>
                  </li>
                )}
              </ul>
            </div>
          );
        })}
      </div>
    </div>
  );
}
