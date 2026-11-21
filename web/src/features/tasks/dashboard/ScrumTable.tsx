import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react';
import { Link } from 'react-router-dom';
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type Announcements,
  type DragEndEvent,
  type Modifier,
  type ScreenReaderInstructions,
  type UniqueIdentifier,
} from '@dnd-kit/core';
import {
  SortableContext,
  horizontalListSortingStrategy,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { Check, GripVertical } from 'lucide-react';
import { UnreadTaskDot } from '@/features/notifications/UnreadTaskDot';
import { Badge } from '@/shared/components/ui/badge';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { cn } from '@/shared/utils/cn';
import { hiddenSides } from '@/shared/utils/hiddenSides';
import { DANGER_TEXT, taskDateLabel } from '../dueDate';
import { cellStatus, moved, taskColumnCount, type ScrumCell, type ScrumTableRow } from './scrumTable';

/**
 * The scrum as a table: a row per person with their lead and their open work
 * across as Task 1, 2, 3, and what they finished since the last scrum at the end.
 *
 * With `onMovePeople` and `onMoveTasks` it can be arranged: each row and each task
 * has a handle to drag by mouse or finger, or to pick up from the keyboard with
 * Space or Enter, carry with the arrow keys and drop with Space or Enter. Without
 * them it is read-only, as a saved scrum is.
 *
 * On a narrow screen the task columns scroll sideways under the names, which stay.
 */

export interface ScrumTableProps {
  rows: ScrumTableRow[];
  caption: string;
  onMovePeople?: (people: string[]) => void;
  onMoveTasks?: (personId: string, tasks: string[]) => void;
}

const HANDLE_W = 'w-8 min-w-8 max-w-8';
// On a phone the name column is narrow and My Lead folds under the name, so a
// task column takes about three quarters of what is left (page padding, border,
// handle and name come to 154px) and the next one peeks in from the right.
const NAME_W = 'w-24 min-w-24 max-w-24 sm:w-48 sm:min-w-48 sm:max-w-48';
const NAME_LEFT = 'left-8';
const LEAD_W = 'hidden sm:table-cell sm:w-32 sm:min-w-32';
const CELL = 'border-b border-border px-2.5 py-2 align-top';
const PHONE_COL = 'w-[calc((100vw-154px)*0.72)] min-w-[calc((100vw-154px)*0.72)] max-w-[calc((100vw-154px)*0.72)]';
const TASK_W = cn(PHONE_COL, 'sm:w-52 sm:min-w-52 sm:max-w-52 md:w-60 md:min-w-60 md:max-w-60');
const DONE_W = cn(PHONE_COL, 'sm:w-56 sm:min-w-56 sm:max-w-56');

const lockX: Modifier = ({ transform }) => ({ ...transform, x: 0 });
const lockY: Modifier = ({ transform }) => ({ ...transform, y: 0 });

const instructions: ScreenReaderInstructions = {
  draggable:
    'To move it, press Space or Enter. Use the arrow keys to carry it, Space or Enter to drop it where it is, or Escape to put it back.',
};

const translate = (t: { x: number; y: number } | null): string | undefined =>
  t ? `translate3d(${Math.round(t.x)}px, ${Math.round(t.y)}px, 0)` : undefined;

/** What a screen reader hears while something is carried: where it is, in words. */
function announcer(items: { id: string; label: string }[], unit: string): Announcements {
  const label = (id: UniqueIdentifier) => items.find((i) => i.id === id)?.label ?? 'It';
  const place = (id: UniqueIdentifier) => `${unit} ${items.findIndex((i) => i.id === id) + 1} of ${items.length}`;
  return {
    onDragStart: ({ active }) => `Picked up ${label(active.id)}, ${place(active.id)}.`,
    onDragOver: ({ active, over }) =>
      over ? `${label(active.id)} is over ${place(over.id)}.` : `${label(active.id)} is not over a place.`,
    onDragEnd: ({ active, over }) =>
      over ? `${label(active.id)} dropped at ${place(over.id)}.` : `${label(active.id)} dropped back.`,
    onDragCancel: ({ active }) => `Moving ${label(active.id)} was cancelled. It is back at ${place(active.id)}.`,
  };
}

function useOrderSensors() {
  return useSensors(
    // Only the handles start a drag, and they take no scrolling, so a finger on
    // one drags straight away and a swipe anywhere else still scrolls the table.
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  );
}

/** The ids with the dragged one dropped where the other was, or null if nothing moved. */
function dropped(ids: string[], { active, over }: DragEndEvent): string[] | null {
  if (!over || active.id === over.id) return null;
  const from = ids.indexOf(String(active.id));
  const to = ids.indexOf(String(over.id));
  return from < 0 || to < 0 ? null : moved(ids, from, to);
}

// The live regions dnd-kit adds go to the page body: a table row cannot hold them.
const accessibility = () => ({
  container: typeof document === 'undefined' ? undefined : document.body,
  screenReaderInstructions: instructions,
});

export function ScrumTable({ rows, caption, onMovePeople, onMoveTasks }: ScrumTableProps) {
  const sensors = useOrderSensors();
  const editable = !!onMovePeople && !!onMoveTasks;
  const columns = taskColumnCount(rows);
  const ids = rows.map((r) => r.userId);
  const scroller = useRef<HTMLDivElement>(null);
  const [moreRight, setMoreRight] = useState(false);
  const measure = useCallback(() => {
    const el = scroller.current;
    if (el) setMoreRight(hiddenSides(el.scrollLeft, el.clientWidth, el.scrollWidth).right);
  }, []);
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    if (el.firstElementChild) observer.observe(el.firstElementChild);
    return () => observer.disconnect();
  }, [measure]);

  const body = rows.map((row) => (
    <PersonRow key={row.userId} row={row} columns={columns} onMoveTasks={editable ? onMoveTasks : undefined} />
  ));

  return (
    <div className="relative overflow-hidden rounded-lg border border-border bg-card">
      {/* Positioned, so the screen-reader-only text in its cells stays inside it. */}
      <div ref={scroller} onScroll={measure} className="relative overflow-x-auto" data-testid="scrum-table">
        <table className="min-w-full border-separate border-spacing-0 text-sm">
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr className="text-left text-xs font-medium text-muted-foreground">
              {editable && (
                <th scope="col" className={cn(CELL, HANDLE_W, 'sticky left-0 z-20 bg-muted px-0')}>
                  <span className="sr-only">Move</span>
                </th>
              )}
              <th
                scope="col"
                className={cn(CELL, NAME_W, 'sticky z-20 border-r bg-muted', editable ? NAME_LEFT : 'left-0')}
              >
                Name
              </th>
              <th scope="col" className={cn(CELL, LEAD_W, 'bg-muted')}>
                My Lead
              </th>
              {Array.from({ length: columns }, (_, i) => (
                <th key={i} scope="col" className={cn(CELL, TASK_W, 'border-l bg-muted')}>
                  Task {i + 1}
                </th>
              ))}
              <th scope="col" className={cn(CELL, DONE_W, 'border-l bg-muted')}>
                Done
              </th>
              {/* Takes up whatever the columns leave, so they keep their widths. */}
              <th aria-hidden className={cn(CELL, 'w-full bg-muted p-0')} />
            </tr>
          </thead>
          {editable ? (
            <DndContext
              sensors={sensors}
              collisionDetection={closestCenter}
              modifiers={[lockX]}
              accessibility={{
                ...accessibility(),
                announcements: announcer(
                  rows.map((r) => ({ id: r.userId, label: r.name })),
                  'row'
                ),
              }}
              onDragEnd={(event) => {
                const next = dropped(ids, event);
                if (next) onMovePeople?.(next);
              }}
            >
              <SortableContext items={ids} strategy={verticalListSortingStrategy}>
                <tbody>{body}</tbody>
              </SortableContext>
            </DndContext>
          ) : (
            <tbody>{body}</tbody>
          )}
        </table>
      </div>
      {/* Says there are more task columns off to the right, before anybody has to guess. */}
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-y-0 right-0 w-10 bg-gradient-to-l from-card to-transparent transition-opacity',
          moreRight ? 'opacity-100' : 'opacity-0'
        )}
      />
    </div>
  );
}

function Handle({
  label,
  sortable,
  className,
}: {
  label: string;
  sortable: ReturnType<typeof useSortable>;
  className?: string;
}) {
  return (
    <button
      type="button"
      ref={sortable.setActivatorNodeRef}
      {...sortable.attributes}
      {...sortable.listeners}
      aria-label={label}
      className={cn(
        'flex touch-none items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        sortable.isDragging ? 'cursor-grabbing' : 'cursor-grab',
        className
      )}
    >
      <GripVertical className="h-4 w-4" aria-hidden />
    </button>
  );
}

function PersonRow({
  row,
  columns,
  onMoveTasks,
}: {
  row: ScrumTableRow;
  columns: number;
  onMoveTasks?: (personId: string, tasks: string[]) => void;
}) {
  const editable = !!onMoveTasks;
  const sortable = useSortable({ id: row.userId, disabled: !editable });
  const sensors = useOrderSensors();
  const taskIds = row.open.map((c) => c.id);
  // While a row is carried it sits above the others, opaque, so the sticky
  // cells it passes under do not show through it.
  const lifted = sortable.isDragging;
  const style: CSSProperties = {
    transform: translate(sortable.transform),
    transition: sortable.transition,
  };
  const stickyBg = lifted ? 'bg-accent' : 'bg-card';

  const cells = row.open.map((c) => <TaskCell key={c.id} cell={c} editable={editable} />);
  const blanks = Array.from({ length: columns - Math.max(row.open.length, 1) }, (_, i) => (
    <td key={`blank-${i}`} className={cn(CELL, TASK_W, 'border-l')} />
  ));
  const blocked = row.counts.blocked;
  const overdue = row.counts.overdue;

  return (
    <tr
      ref={sortable.setNodeRef}
      style={style}
      data-person={row.userId}
      className={cn(lifted && 'relative z-30 bg-accent shadow-lg')}
    >
      {editable && (
        <td className={cn(CELL, HANDLE_W, 'sticky left-0 z-10 px-0', stickyBg)}>
          <Handle label={`Move ${row.name}`} sortable={sortable} className="mx-auto h-8 w-7" />
        </td>
      )}
      <th
        scope="row"
        className={cn(
          CELL,
          NAME_W,
          'sticky z-10 border-r text-left font-normal',
          editable ? NAME_LEFT : 'left-0',
          stickyBg
        )}
      >
        <div className="flex items-start gap-2">
          <PersonAvatar
            userId={row.userId}
            name={row.name}
            className="hidden h-7 w-7 shrink-0 sm:flex"
            fallbackClassName="text-[10px]"
            decorative
          />
          <div className="min-w-0 flex-1">
            <p
              className="truncate font-semibold leading-snug sm:whitespace-normal sm:break-anywhere"
              title={row.name}
              data-testid="scrum-person-name"
            >
              {row.name}
            </p>
            {row.teams.length > 0 && (
              <p className="break-anywhere text-xs text-muted-foreground">{row.teams.map((t) => t.name).join(', ')}</p>
            )}
            {/* My Lead is a column from sm up; on a phone it is a line here instead. */}
            {!!row.leads?.length && (
              <p className="break-anywhere text-xs text-muted-foreground sm:hidden" data-testid="scrum-leads-inline">
                Lead: {row.leads.join(', ')}
              </p>
            )}
            {(blocked > 0 || overdue > 0) && (
              <p className={cn('text-xs tabular-nums', DANGER_TEXT)}>
                {[blocked > 0 && `${blocked} blocked`, overdue > 0 && `${overdue} overdue`].filter(Boolean).join(', ')}
              </p>
            )}
          </div>
        </div>
      </th>
      <td className={cn(CELL, LEAD_W, 'break-anywhere')} data-testid="scrum-leads">
        {row.leads === null ? (
          <span className="text-xs text-muted-foreground">Not recorded</span>
        ) : row.leads.length > 0 ? (
          row.leads.join(', ')
        ) : (
          <span className="sr-only">No lead</span>
        )}
      </td>
      {row.open.length === 0 ? (
        <td className={cn(CELL, TASK_W, 'border-l text-muted-foreground')}>No open tasks</td>
      ) : editable ? (
        <DndContext
          sensors={sensors}
          collisionDetection={closestCenter}
          modifiers={[lockY]}
          accessibility={{
            ...accessibility(),
            announcements: announcer(
              row.open.map((c) => ({ id: c.id, label: c.title })),
              `${row.name}'s task`
            ),
          }}
          onDragEnd={(event) => {
            const next = dropped(taskIds, event);
            if (next) onMoveTasks?.(row.userId, next);
          }}
        >
          <SortableContext items={taskIds} strategy={horizontalListSortingStrategy}>
            {cells}
          </SortableContext>
        </DndContext>
      ) : (
        cells
      )}
      {blanks}
      <DoneCell done={row.done} />
      <td aria-hidden className={cn(CELL, 'p-0')} />
    </tr>
  );
}

function TaskCell({ cell, editable }: { cell: ScrumCell; editable: boolean }) {
  const sortable = useSortable({ id: cell.id, disabled: !editable });
  const status = cellStatus(cell);
  const date = taskDateLabel(cell);
  const pressing = cell.priority === 'urgent' || cell.priority === 'high';
  const warn = status.danger || !!date?.overdue;
  const style: CSSProperties = {
    transform: translate(sortable.transform),
    transition: sortable.transition,
  };

  return (
    <td
      ref={sortable.setNodeRef}
      style={style}
      data-task={cell.id}
      className={cn(
        CELL,
        TASK_W,
        'border-l',
        // A bar down the left edge, so a stuck or late task is seen before it is read.
        warn && 'shadow-[inset_3px_0_0_theme(colors.red.500)]',
        sortable.isDragging && 'relative z-20 bg-accent shadow-lg'
      )}
    >
      <div className="flex items-start gap-1">
        {editable && <Handle label={`Move ${cell.title}`} sortable={sortable} className="-ml-1.5 h-6 w-5 shrink-0" />}
        <div className="min-w-0 flex-1">
          <div className="flex items-start gap-1.5">
            <Link
              to={`/tasks/${cell.id}`}
              title={cell.title}
              className="line-clamp-3 min-w-0 break-anywhere font-medium leading-snug hover:underline"
            >
              {cell.title}
            </Link>
            <UnreadTaskDot taskId={cell.id} className="mt-1.5 shrink-0" />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <span className={cn('font-medium', status.danger && DANGER_TEXT)}>{status.label}</span>
            {date && <span className={cn('tabular-nums', date.overdue && DANGER_TEXT)}>{date.text}</span>}
            {pressing && (
              <Badge variant="outline" className="px-1.5 py-0 font-medium capitalize">
                {cell.priority}
              </Badge>
            )}
            {cell.addedTo && (
              <Badge variant="outline" className="border-dashed px-1.5 py-0 font-medium">
                Added to
              </Badge>
            )}
            {cell.isNew && (
              <Badge variant="secondary" className="px-1.5 py-0">
                New
              </Badge>
            )}
          </div>
        </div>
      </div>
    </td>
  );
}

const DONE_SHOWN = 3;

function DoneCell({ done }: { done: ScrumCell[] }) {
  const [all, setAll] = useState(false);
  const shown = all ? done : done.slice(0, DONE_SHOWN);
  return (
    <td className={cn(CELL, DONE_W, 'border-l')} data-testid="scrum-done">
      {done.length === 0 ? (
        <span className="sr-only">Nothing done</span>
      ) : (
        <>
          <ul className="space-y-1">
            {shown.map((c) => (
              <li key={c.id} className="flex items-start gap-1.5">
                <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-green-600 dark:text-green-400" aria-hidden />
                <Link
                  to={`/tasks/${c.id}`}
                  title={c.title}
                  className="line-clamp-2 min-w-0 break-anywhere hover:underline"
                >
                  {c.title}
                </Link>
              </li>
            ))}
          </ul>
          {done.length > DONE_SHOWN && (
            <button
              type="button"
              className="mt-1 text-xs font-medium text-muted-foreground hover:text-foreground hover:underline"
              aria-expanded={all}
              onClick={() => setAll((v) => !v)}
            >
              {all ? 'Show fewer' : `Show ${done.length - DONE_SHOWN} more`}
            </button>
          )}
        </>
      )}
    </td>
  );
}
