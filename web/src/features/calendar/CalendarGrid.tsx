import { useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { format, isSameDay } from 'date-fns';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { cn, formatTime } from '@/shared/utils/cn';
import { meetingStillOn,
  MIN_EVENT_MINUTES,
  dayLabelKey,
  itemKey,
  itemLabel,
  durationLabel,
  layoutDay,
  overrunMinutes,
  nowOffset,
  segmentsForDay,
  slotFromOffset,
  type CalendarItem,
  type CalendarView,
  type OverflowChip,
  type PlacedSegment,
  type Segment,
} from './layout';

/** Pixels per hour. Twenty minutes comes to one readable line of text. */
const HOUR_HEIGHT = 56;
const HOURS = Array.from({ length: 24 }, (_, hour) => hour);
const pixels = (minutes: number) => (minutes / 60) * HOUR_HEIGHT;
/** A strip of an open slot stays showing beside whatever is inside it. */
const INSET_PERCENT = 12;

const EVENT_STYLE: Record<'busy' | 'focus', string> = {
  busy: 'border-primary/60 bg-primary/15 text-foreground hover:bg-primary/25 dark:bg-primary/25 dark:hover:bg-primary/35',
  focus: 'border-amber-500/70 bg-amber-100 text-amber-950 hover:bg-amber-200 dark:bg-amber-900/50 dark:text-amber-50 dark:hover:bg-amber-900/70',
};

function rangeText(item: CalendarItem): string {
  const range = `${formatTime(item.startsAt)} to ${formatTime(item.endsAt)}`;
  const over = overrunMinutes(item);
  return over > 0 ? `${range}, extended ${durationLabel(over)}` : range;
}

/** The part of a block past the meeting's planned end, striped so it reads without colour. */
function Overrun({ segment }: { segment: Segment }) {
  if (segment.overrunFromMinute === undefined) return null;
  const top = pixels(segment.overrunFromMinute - segment.startMinute);
  const tall = pixels(segment.endMinute - segment.overrunFromMinute) >= 16;
  return (
    <span
      aria-hidden="true"
      data-testid="overrun"
      style={{ top }}
      className="pointer-events-none absolute inset-x-0 bottom-0 flex items-end border-t border-dashed border-current bg-[repeating-linear-gradient(135deg,transparent,transparent_4px,hsl(var(--primary)/0.28)_4px,hsl(var(--primary)/0.28)_8px)] px-1 pb-0.5"
    >
      {tall && <span className="truncate text-[10px] font-semibold uppercase tracking-wide">Extended</span>}
    </span>
  );
}

/** A week, or a day, of hours in a CSS grid. Times are the browser's. */
export function CalendarGrid({
  days,
  items,
  now,
  view,
  onSelect,
  onEmptyClick,
  emptyClickLabel,
}: {
  days: Date[];
  items: CalendarItem[];
  now: Date;
  view: CalendarView;
  onSelect: (item: CalendarItem) => void;
  /** Left out for a calendar that is only to be read, such as a report's. */
  onEmptyClick?: (slot: { start: Date; end: Date }) => void;
  emptyClickLabel?: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [pile, setPile] = useState<Segment[] | null>(null);

  // Start around the working day rather than at midnight, once.
  useEffect(() => {
    const element = scrollRef.current;
    if (!element) return;
    const hour = Math.max(now.getHours() - 1, 0);
    element.scrollTop = pixels(Math.min(hour, 8) * 60);
    // Only on arriving; moving through weeks should keep your place.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const columns = `3.25rem repeat(${days.length}, minmax(0, 1fr))`;

  return (
    <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden" tabIndex={0} aria-label="Hours of the day">
      {view === 'week' && (
        <div
          className="sticky top-0 z-20 grid border-b border-border bg-background"
          style={{ gridTemplateColumns: columns }}
        >
          <div />
          {days.map((day) => {
            const today = isSameDay(day, now);
            return (
              <div key={dayLabelKey(day)} className="border-l border-border py-1.5 text-center">
                <div className="text-xs text-muted-foreground">{format(day, 'EEE')}</div>
                <div
                  className={cn(
                    'mx-auto flex h-7 w-7 items-center justify-center rounded-full text-sm font-medium tabular-nums',
                    today && 'bg-primary text-primary-foreground'
                  )}
                  aria-current={today ? 'date' : undefined}
                >
                  {format(day, 'd')}
                </div>
              </div>
            );
          })}
        </div>
      )}

      <div className="grid" style={{ gridTemplateColumns: columns, height: HOUR_HEIGHT * 24 }}>
        <div className="relative" aria-hidden="true">
          {HOURS.map((hour) =>
            hour === 0 ? null : (
              <span
                key={hour}
                className="absolute right-1.5 -translate-y-1/2 text-[11px] tabular-nums text-muted-foreground"
                style={{ top: hour * HOUR_HEIGHT }}
              >
                {format(new Date(2000, 0, 1, hour), 'h a')}
              </span>
            )
          )}
        </div>
        {days.map((day) => (
          <DayColumn
            key={dayLabelKey(day)}
            day={day}
            items={items}
            now={now}
            view={view}
            onSelect={onSelect}
            onPile={setPile}
            onEmptyClick={onEmptyClick}
            emptyClickLabel={emptyClickLabel}
          />
        ))}
      </div>

      <Dialog open={!!pile} onOpenChange={(open) => !open && setPile(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Also at this time</DialogTitle>
            <DialogDescription>These overlap too much to fit side by side.</DialogDescription>
          </DialogHeader>
          <ul className="space-y-1">
            {pile?.map((segment) => (
              <li key={itemKey(segment.item, segment.index)}>
                <button
                  type="button"
                  className="w-full rounded-md border border-border px-3 py-2 text-left text-sm hover:bg-accent"
                  onClick={() => {
                    setPile(null);
                    onSelect(segment.item);
                  }}
                >
                  <span className="block break-anywhere font-medium">{itemLabel(segment.item)}</span>
                  <span className="block text-xs tabular-nums text-muted-foreground">{rangeText(segment.item)}</span>
                </button>
              </li>
            ))}
          </ul>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function DayColumn({
  day,
  items,
  now,
  view,
  onSelect,
  onPile,
  onEmptyClick,
  emptyClickLabel,
}: {
  day: Date;
  items: CalendarItem[];
  now: Date;
  view: CalendarView;
  onSelect: (item: CalendarItem) => void;
  onPile: (segments: Segment[]) => void;
  onEmptyClick?: (slot: { start: Date; end: Date }) => void;
  emptyClickLabel?: string;
}) {
  // A wide day has room for more side by side than a seventh of a screen. Beyond
  // this a name is down to its first letters, so the rest go in a chip.
  const maxColumns = view === 'day' ? 3 : 2;
  const layout = useMemo(() => layoutDay(segmentsForDay(items, day), { maxColumns }), [items, day, maxColumns]);
  const offset = nowOffset(now, day);

  const clicked = (event: MouseEvent<HTMLDivElement>) => {
    const box = event.currentTarget.getBoundingClientRect();
    const minute = ((event.clientY - box.top) / HOUR_HEIGHT) * 60;
    onEmptyClick?.(slotFromOffset(day, minute));
  };

  return (
    <div className="relative border-l border-border" role="group" aria-label={format(day, 'EEEE, MMMM d')}>
      {/* The hour lines are the thing you click. Events sit on top of them. */}
      <div
        className={cn('absolute inset-0', onEmptyClick && 'cursor-pointer')}
        title={onEmptyClick ? emptyClickLabel : undefined}
        onClick={onEmptyClick ? clicked : undefined}
      >
        {HOURS.map((hour) => (
          <div
            key={hour}
            className={cn('absolute inset-x-0 border-t border-border/60', hour === 0 && 'border-t-0')}
            style={{ top: hour * HOUR_HEIGHT }}
          />
        ))}
      </div>

      {layout.background.map((segment) => (
        <OpenSlot key={itemKey(segment.item, segment.index)} segment={segment} onSelect={onSelect} />
      ))}

      {layout.events.map((segment) => (
        <EventBlock key={itemKey(segment.item, segment.index)} segment={segment} onSelect={onSelect} />
      ))}

      {layout.overflow.map((chip) => (
        <Overflow key={`${chip.startMinute}-${chip.column}`} chip={chip} onPile={onPile} />
      ))}

      {offset !== null && (
        <div
          className="pointer-events-none absolute inset-x-0 z-10 border-t-2 border-red-500"
          style={{ top: pixels(offset) }}
          aria-label={`Now, ${formatTime(now)}`}
          role="img"
        >
          <span className="absolute -left-1 -top-[5px] h-2 w-2 rounded-full bg-red-500" />
        </div>
      )}
    </div>
  );
}

function positionStyle(startMinute: number, endMinute: number) {
  return {
    top: pixels(startMinute),
    height: Math.max(pixels(endMinute - startMinute), pixels(MIN_EVENT_MINUTES)) - 2,
  };
}

function OpenSlot({ segment, onSelect }: { segment: Segment; onSelect: (item: CalendarItem) => void }) {
  const { item } = segment;
  return (
    <button
      type="button"
      onClick={() => onSelect(item)}
      aria-label={`${itemLabel(item)}, ${rangeText(item)}`}
      style={positionStyle(segment.startMinute, segment.endMinute)}
      className="absolute inset-x-0.5 flex flex-col justify-start overflow-hidden rounded border border-dashed border-emerald-500/70 bg-[repeating-linear-gradient(45deg,transparent,transparent_6px,rgb(16_185_129/0.12)_6px,rgb(16_185_129/0.12)_12px)] px-1 py-0.5 text-left text-[11px] leading-tight text-emerald-800 hover:bg-emerald-500/10 dark:text-emerald-300"
    >
      <span className="block w-full truncate font-medium">{itemLabel(item)}</span>
    </button>
  );
}

function EventBlock({ segment, onSelect }: { segment: PlacedSegment; onSelect: (item: CalendarItem) => void }) {
  const { item, column, columns, inset } = segment;
  const area = 100 - (inset ? INSET_PERCENT : 0);
  const width = area / columns;
  const left = (inset ? INSET_PERCENT : 0) + column * width;
  // The time only when there is width for it to be read.
  const showTime = segment.endMinute - segment.startMinute >= 40 && columns === 1;
  const finished =
    item.status === 'ended' &&
    !(item.source === 'meeting' && meetingStillOn({ status: item.status, scheduledEnd: item.endsAt }, new Date()));
  const kind = item.kind === 'focus' ? 'focus' : 'busy';

  return (
    <button
      type="button"
      onClick={() => onSelect(item)}
      aria-label={`${itemLabel(item)}, ${rangeText(item)}`}
      style={{ ...positionStyle(segment.startMinute, segment.endMinute), left: `calc(${left}% + 1px)`, width: `calc(${width}% - 2px)` }}
      className={cn(
        'absolute z-[1] flex flex-col justify-start overflow-hidden rounded border-l-2 px-1 py-0.5 text-left text-[11px] leading-tight shadow-sm transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        EVENT_STYLE[kind],
        finished && 'opacity-70'
      )}
    >
      <Overrun segment={segment} />
      <span className="relative block w-full truncate font-medium">{itemLabel(item)}</span>
      {showTime && <span className="relative block w-full truncate tabular-nums opacity-80">{rangeText(item)}</span>}
    </button>
  );
}

function Overflow({ chip, onPile }: { chip: OverflowChip; onPile: (segments: Segment[]) => void }) {
  const width = 100 / chip.columns;
  const label = `${chip.segments.length} more at this time`;
  return (
    <button
      type="button"
      onClick={() => onPile(chip.segments)}
      aria-label={label}
      title={label}
      style={{
        ...positionStyle(chip.startMinute, chip.endMinute),
        left: `calc(${chip.column * width}% + 1px)`,
        width: `calc(${width}% - 2px)`,
      }}
      className="absolute z-[1] flex items-start justify-center overflow-hidden rounded border border-border bg-muted px-0.5 py-0.5 text-[11px] font-medium hover:bg-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      +{chip.segments.length}
    </button>
  );
}

