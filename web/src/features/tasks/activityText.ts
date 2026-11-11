/**
 * How one line of a task's history reads. The server records the field's own
 * name and the raw values, which is right for keeping but not for reading:
 * "changed assigneeId to 3f2c…" says nothing to anybody.
 */

export interface ActivityFacts {
  field: string;
  oldValue?: string | null;
  newValue?: string | null;
}

export interface ActivityLine {
  /** What was done, after the person's name: "commented", "changed status". */
  verb: string;
  from?: string;
  to?: string;
}

const FIELD_NAMES: Record<string, string> = {
  assigneeId: 'the assignee',
  dueDate: 'the due date',
  startDate: 'the start date',
  storyPoints: 'story points',
  taskType: 'the type',
  parentTaskId: 'the parent task',
  title: 'the title',
  description: 'the description',
  status: 'status',
  priority: 'priority',
};

const STATUS_WORDS: Record<string, string> = {
  todo: 'To do',
  in_progress: 'In progress',
  in_review: 'In review',
  done: 'Done',
  blocked: 'Blocked',
};

function day(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

export function activityLine(a: ActivityFacts, nameOf: (userId: string) => string | undefined): ActivityLine {
  const value = (raw: string | null | undefined): string | undefined => {
    if (!raw) return undefined;
    switch (a.field) {
      case 'status':
        return STATUS_WORDS[raw] ?? raw;
      case 'dueDate':
      case 'startDate':
        return day(raw);
      case 'assigneeId':
        return nameOf(raw) ?? 'somebody';
      case 'parentTaskId':
        return undefined;
      default:
        return raw;
    }
  };
  if (a.field === 'comment') return { verb: 'commented', to: a.newValue || undefined };
  if (a.field === 'position') return { verb: 'moved it on the board' };
  if (a.field === 'assigneeId' && !a.newValue) return { verb: 'unassigned it', from: value(a.oldValue) };
  if ((a.field === 'dueDate' || a.field === 'startDate') && !a.newValue) {
    return { verb: `removed ${FIELD_NAMES[a.field]}`, from: value(a.oldValue) };
  }
  return { verb: `changed ${FIELD_NAMES[a.field] ?? a.field}`, from: value(a.oldValue), to: value(a.newValue) };
}
