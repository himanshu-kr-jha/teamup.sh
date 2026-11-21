import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { FormDialog } from '@/shared/components/FormDialog';
import { SearchSelect, usePersonOptions, type SelectOption } from '@/shared/components/SearchSelect';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { orgApi, tasksApi } from '@/shared/api/client';
import type { BlockedOn, BlockedOnInput } from '@/shared/types/dashboard';
import { cn } from '@/shared/utils/cn';
import { DANGER_TEXT } from './dueDate';

/** The server keeps up to this many characters of a note. */
export const BLOCKED_NOTE_MAX = 1000;
const NOBODY = '';

/**
 * Asks what a task is blocked on, whenever it is marked Blocked and whenever
 * the reason is changed: a short note, which is required here, and optionally
 * the person and the task it is waiting for.
 */
export function BlockedOnDialog({
  open,
  onOpenChange,
  taskId,
  taskTitle,
  current,
  editing = false,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  taskId: string;
  taskTitle: string;
  /** The reason it has now, when it is being changed. */
  current?: BlockedOn | null;
  /** Changing the reason of a task that is already blocked, rather than blocking it. */
  editing?: boolean;
  onSave: (blockedOn: BlockedOnInput) => Promise<void>;
}) {
  const [note, setNote] = useState('');
  const [userId, setUserId] = useState(NOBODY);
  const [waitTaskId, setWaitTaskId] = useState(NOBODY);

  useEffect(() => {
    if (!open) return;
    setNote(current?.note ?? '');
    setUserId(current?.user?.id ?? NOBODY);
    setWaitTaskId(current?.task?.id ?? NOBODY);
  }, [open, current]);

  const { data: users } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: open });
  const people = useMemo(
    () => ((users?.data ?? []) as Array<{ id: string; name: string; email?: string }>).map((u) => ({ id: u.id, name: u.name, email: u.email })),
    [users]
  );
  const personOptions = usePersonOptions(people, { extra: [{ value: NOBODY, label: 'Nobody in particular' }] });

  const { data: tasks } = useQuery({
    queryKey: ['tasks', 'blocked-on-choices'],
    queryFn: () => tasksApi.list(),
    enabled: open,
    staleTime: 30_000,
  });
  const taskOptions: SelectOption[] = useMemo(() => {
    const open = ((tasks?.data ?? []) as Array<{ id: string; title: string; key?: string; status: string }>).filter(
      (t) => t.id !== taskId && t.status !== 'done'
    );
    return [
      { key: 'none', value: NOBODY, label: 'No task', facts: { primary: ['No task'] } },
      ...open.map((t) => ({
        key: t.id,
        value: t.id,
        label: t.key ? `${t.key} ${t.title}` : t.title,
        facts: { primary: [t.title, t.key] },
      })),
    ];
  }, [tasks, taskId]);

  const trimmed = note.trim();
  const tooLong = trimmed.length > BLOCKED_NOTE_MAX;
  // Somebody choosing the task it waits for has said what it is blocked on
  // even before the waiting task's title loads; the note is still asked for.
  const waitTaskLabel = current?.task && current.task.id === waitTaskId ? current.task.title : undefined;

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? 'What is it blocked on?' : 'Mark blocked'}
      description={`Say what "${taskTitle}" is waiting for. It shows on the task and in the scrum.`}
      submitLabel={editing ? 'Save' : 'Mark blocked'}
      disabledReason={!trimmed ? 'Say what it is blocked on' : tooLong ? 'That note is too long' : undefined}
      onSubmit={() =>
        onSave({
          note: trimmed,
          userId: userId || null,
          taskId: waitTaskId || null,
        })
      }
    >
      <div className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="blockedNote">Blocked on</Label>
          <Textarea
            id="blockedNote"
            rows={3}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="Waiting for the vendor's API keys"
            autoFocus
          />
          {tooLong && (
            <p className={cn('text-right text-xs tabular-nums', DANGER_TEXT)}>
              {trimmed.length} / {BLOCKED_NOTE_MAX}
            </p>
          )}
        </div>
        <div className="space-y-2">
          <Label htmlFor="blockedUser">Waiting on a person (optional)</Label>
          <SearchSelect
            id="blockedUser"
            value={userId}
            onChange={setUserId}
            options={personOptions}
            searchPlaceholder="Search people"
            nobody="Nobody in particular"
            className="h-9 w-full"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="blockedTask">Waiting for a task (optional)</Label>
          <SearchSelect
            id="blockedTask"
            value={waitTaskId}
            onChange={setWaitTaskId}
            options={taskOptions}
            display={waitTaskLabel}
            searchPlaceholder="Search tasks"
            nobody="No task"
            className="h-9 w-full"
          />
        </div>
      </div>
    </FormDialog>
  );
}

/** A reason in a line, for a card or a cell: the note, then who and what it waits on. */
export function blockedOnWords(b: { note?: string | null; who?: string | null; task?: string | null }): string {
  const parts = [b.note?.trim(), b.who ? `waiting on ${b.who}` : '', b.task ? `waiting for ${b.task}` : ''].filter(Boolean);
  return parts.length ? parts.join('; ') : 'No reason given';
}
