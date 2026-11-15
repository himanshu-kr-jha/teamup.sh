import type { ReactNode } from 'react';
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  MeasuringStrategy,
  closestCenter,
  pointerWithin,
  type CollisionDetection,
  MouseSensor,
  TouchSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type Announcements,
  type DragEndEvent,
  type DragStartEvent,
  type KeyboardCoordinateGetter,
  type Modifier,
} from '@dnd-kit/core';
import { cn } from '@/shared/utils/cn';

/**
 * Dragging a task between the board's columns changes its status.
 *
 * The same board is drawn twice, once for a phone (one column at a time, with
 * the columns as chips above it) and once for anything wider, and only one is
 * visible. Every draggable and droppable id therefore carries which of the two
 * it belongs to, so the hidden copy never collides with the visible one.
 *
 * A mouse picks a card up after it has moved a few pixels, so a click still
 * opens the task. A finger has to rest on the card for a moment first, so a
 * swipe still scrolls the list. The keyboard works too: Space or Enter picks
 * the focused card up, the arrows carry it, Space drops it, Escape puts it back.
 */

export type Surface = 'phone' | 'wide';

export const dragId = (surface: Surface, taskId: string) => `${surface}:task:${taskId}`;
export const dropId = (surface: Surface, status: string) => `${surface}:column:${status}`;

/** The status a drop target stands for, or null if it is not a column. */
export function statusOfDrop(id: string | number | undefined | null): string | null {
  if (typeof id !== 'string') return null;
  const m = /^(?:phone|wide):column:(.+)$/.exec(id);
  return m ? m[1] : null;
}

export interface DragData {
  taskId: string;
  status: string;
  title: string;
}

/**
 * Decides what a finished drag means: the task and the status it should move
 * to, or null when it was dropped nowhere, or back where it started.
 */
export function moveOf(data: DragData | undefined, overId: string | number | undefined | null) {
  const to = statusOfDrop(overId);
  if (!data || !to || to === data.status) return null;
  return { taskId: data.taskId, from: data.status, to };
}

/**
 * Where the arrow keys carry a card: the next column left or right, whole
 * columns at a time. The default moved it 25 pixels per press, so reaching
 * the next column took a dozen presses with nothing to say how close it was.
 */
export const columnKeyboardCoordinates: KeyboardCoordinateGetter = (event, { currentCoordinates, context }) => {
  const dir = event.code === 'ArrowRight' ? 1 : event.code === 'ArrowLeft' ? -1 : 0;
  // Until the first move there is no collision rectangle yet, and the first
  // press used to be lost waiting for one; the card's own box is the same place.
  const here = context.collisionRect ?? context.activeNode?.getBoundingClientRect() ?? null;
  if (!dir || !here) return undefined;
  event.preventDefault();
  const hereX = here.left + here.width / 2;
  const hereY = here.top + here.height / 2;
  let best: { x: number; y: number; d: number } | null = null;
  for (const container of context.droppableContainers.getEnabled()) {
    if (statusOfDrop(container.id) === null) continue;
    const rect = context.droppableRects.get(container.id);
    // The copy of the board that is not on screen has no size.
    if (!rect || rect.width === 0 || rect.height === 0) continue;
    const x = rect.left + rect.width / 2;
    const d = (x - hereX) * dir;
    if (d <= 1) continue;
    if (!best || d < best.d) best = { x, y: Math.min(Math.max(hereY, rect.top + 40), rect.top + rect.height / 2), d };
  }
  if (!best) return currentCoordinates;
  return { x: currentCoordinates.x + (best.x - hereX), y: currentCoordinates.y + (best.y - hereY) };
};

/**
 * Hangs the card being dragged just below the pointer or finger, centred on
 * it. Picked up by one corner, a card as wide as a phone was carried mostly off
 * the screen, and drawn over the finger it hid the chip it was about to land on.
 */
export const centreOnPointer: Modifier = ({ activatorEvent, draggingNodeRect, transform }) => {
  if (!activatorEvent || !draggingNodeRect) return transform;
  const e = activatorEvent as MouseEvent & TouchEvent;
  const point = 'touches' in e && e.touches?.length ? e.touches[0] : 'clientX' in e ? e : null;
  if (!point) return transform;
  return {
    ...transform,
    x: transform.x + point.clientX - draggingNodeRect.left - draggingNodeRect.width / 2,
    y: transform.y + point.clientY - draggingNodeRect.top + 20,
  };
};

/**
 * The column under the pointer or finger is the one it lands in, whatever the
 * card it carries overlaps. The keyboard has no pointer, and takes the column
 * nearest the card.
 */
export const columnUnderPointer: CollisionDetection = (args) =>
  args.pointerCoordinates ? pointerWithin(args) : closestCenter(args);

export function BoardDnd({
  children,
  titleOf,
  onMove,
  onDragStart,
  onDragEnd,
}: {
  children: ReactNode;
  /** The column's name for a status, for what a screen reader is told. */
  titleOf: (status: string) => string;
  onMove: (move: { taskId: string; from: string; to: string }) => void;
  onDragStart?: (data: DragData) => void;
  onDragEnd?: () => void;
}) {
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 220, tolerance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: columnKeyboardCoordinates }),
  );

  const titleOfData = (d: unknown) => (d as DragData | undefined)?.title ?? 'The task';
  const where = (id: string | number | undefined) => {
    const s = statusOfDrop(id);
    return s ? titleOf(s) : null;
  };
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Picked up ${titleOfData(active.data.current)}.`,
    onDragOver: ({ active, over }) =>
      where(over?.id) ? `${titleOfData(active.data.current)} is over ${where(over?.id)}.` : `${titleOfData(active.data.current)} is not over a column.`,
    onDragEnd: ({ active, over }) =>
      where(over?.id) ? `${titleOfData(active.data.current)} moved to ${where(over?.id)}.` : `${titleOfData(active.data.current)} was put back.`,
    onDragCancel: ({ active }) => `${titleOfData(active.data.current)} was put back.`,
  };

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={columnUnderPointer}
      // The phone's column chips rearrange themselves while a card is held, so
      // where they are is measured as it changes, not once at the start.
      measuring={{ droppable: { strategy: MeasuringStrategy.Always } }}
      // A strip marked so never scrolls under a held card: the chips sliding
      // sideways meant the card was dropped on whichever one slid under it.
      autoScroll={{ canScroll: (el) => !(el as HTMLElement).dataset?.noAutoscroll }}
      accessibility={{
        announcements,
        screenReaderInstructions: {
          draggable:
            'To move this task to another column, press Space or Enter, use the left and right arrow keys to reach the column, and press Space again to drop it. Escape puts it back.',
        },
      }}
      onDragStart={(e: DragStartEvent) => {
        const data = e.active.data.current as DragData | undefined;
        if (data) onDragStart?.(data);
      }}
      onDragEnd={(e: DragEndEvent) => {
        const move = moveOf(e.active.data.current as DragData | undefined, e.over?.id);
        onDragEnd?.();
        if (move) onMove(move);
      }}
      onDragCancel={() => onDragEnd?.()}
    >
      {children}
    </DndContext>
  );
}

/** A card that can be picked up. Disabled cards render as they always did. */
export function DraggableTask({
  surface,
  data,
  disabled,
  children,
}: {
  surface: Surface;
  data: DragData;
  disabled?: boolean;
  children: ReactNode;
}) {
  const { setNodeRef, attributes, listeners, isDragging } = useDraggable({
    id: dragId(surface, data.taskId),
    data,
    disabled,
  });
  return (
    <div
      ref={setNodeRef}
      {...(disabled ? {} : attributes)}
      {...(disabled ? {} : listeners)}
      // The card itself stays behind as a faint placeholder while its copy
      // follows the pointer, so the gap it will leave is visible.
      className={cn(
        'rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
        !disabled && 'cursor-grab touch-manipulation select-none active:cursor-grabbing',
        isDragging && 'opacity-40',
      )}
    >
      {children}
    </div>
  );
}

/** Something a task can be dropped on to take that status. */
export function DropColumn({
  surface,
  status,
  className,
  overClassName,
  children,
  as: Tag = 'div',
  ...rest
}: {
  surface: Surface;
  status: string;
  className?: string;
  overClassName?: string;
  children: ReactNode;
  as?: 'div' | 'button';
} & Record<string, unknown>) {
  const { setNodeRef, isOver, active } = useDroppable({ id: dropId(surface, status) });
  const fromHere = (active?.data.current as DragData | undefined)?.status === status;
  return (
    <Tag
      ref={setNodeRef as never}
      className={cn(className, isOver && !fromHere && (overClassName ?? 'ring-2 ring-inset ring-primary'))}
      data-drop-over={isOver && !fromHere ? '' : undefined}
      {...rest}
    >
      {children}
    </Tag>
  );
}

export { DragOverlay };
