import { useState } from 'react';
import { Search, Filter, MoreVertical, Calendar, Lock } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Badge } from '@/shared/components/ui/badge';
import { LabelBadge } from './LabelBadge';
import { cn } from '@/shared/utils/cn';
import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { tasksApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { restrictionNote, type TaskAbilities } from './abilities';
import { DANGER_TEXT, dueLabel } from './dueDate';
import { UnreadTaskDot } from '@/features/notifications/UnreadTaskDot';
import { liveTaskQuery } from './liveQuery';
import { ErrorState, LoadingState } from './dashboard/parts';
import { PersonAvatar } from '@/shared/components/UserAvatar';

interface Task {
  id: string;
  key?: string;
  title: string;
  description: string;
  status: 'todo' | 'in_progress' | 'in_review' | 'done' | 'blocked';
  priority: 'low' | 'medium' | 'high' | 'urgent';
  taskType: 'task' | 'bug' | 'story' | 'epic' | 'subtask';
  assigneeId?: string;
  assigneeName?: string;
  assigneeAvatar?: string;
  dueDate?: string;
  labels: { id?: string; name: string; color: string }[];
  projectId?: string;
  projectName?: string;
  createdAt: string;
  updatedAt: string;
  /** What this caller may do to it, worked out by the server. */
  can?: TaskAbilities;
}

const statusColors = {
  todo: 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200',
  in_progress: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200',
  in_review: 'bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200',
  done: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200',
  blocked: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200',
};

const priorityColors = {
  low: 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200',
  medium: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200',
  high: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200',
  urgent: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200',
};

// Spelled out in full, class by class: Tailwind only generates what it can read
// in the source, so building these by adding '/50' to a name produced nothing.
const columns = [
  { id: 'todo', title: 'To Do', head: 'bg-gray-100 dark:bg-gray-800', body: 'bg-gray-100/50 dark:bg-gray-800/30' },
  { id: 'in_progress', title: 'In Progress', head: 'bg-blue-100 dark:bg-blue-950', body: 'bg-blue-100/50 dark:bg-blue-950/30' },
  { id: 'in_review', title: 'In Review', head: 'bg-purple-100 dark:bg-purple-950', body: 'bg-purple-100/50 dark:bg-purple-950/30' },
  { id: 'blocked', title: 'Blocked', head: 'bg-red-100 dark:bg-red-950', body: 'bg-red-100/50 dark:bg-red-950/30' },
  { id: 'done', title: 'Done', head: 'bg-green-100 dark:bg-green-950', body: 'bg-green-100/50 dark:bg-green-950/30' },
];

/**
 * The board. `managesSomebody` comes from the scope the server gave the
 * dashboard, so the Team view appears for exactly the people the rest of the
 * page treats as managers, with no second opinion worked out from roles here.
 */
export function BoardView({ managesSomebody }: { managesSomebody: boolean }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [viewMode, setViewMode] = useState<'list' | 'board'>('board');
  const [scope, setScope] = useState<'mine' | 'team' | 'all'>('mine');
  const [teamId, setTeamId] = useState('all');
  const [assigneeId, setAssigneeId] = useState('all');
  // Which of your own: what is yours to do, what you handed out, or what you
  // were added to. Kept apart, or a manager's list is their people's work.
  const [mineAs, setMineAs] = useState<'assigned' | 'created' | 'added'>('assigned');
  // Which column a phone is showing. Separate from the status filter, which is
  // the wide screen's way of narrowing a board that shows every column at once.
  const [phoneColumn, setPhoneColumn] = useState('todo');
  const [searchOpen, setSearchOpen] = useState(false);
  // One label, by name, or all: labels sort tasks into kinds across projects.
  const [labelFilter, setLabelFilter] = useState('all');
  const { data: labelsData } = useQuery({ queryKey: ['labels'], queryFn: () => tasksApi.listLabels() });
  const allLabels = ((labelsData?.data ?? []) as { id: string; name: string; color: string }[])
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name));

  const { data: managedData } = useQuery({
    queryKey: ['managed-teams'],
    queryFn: () => tasksApi.managedTeams(),
  });
  const managedTeams = managedData?.data ?? [];

  // Narrowing to a team narrows who you can pick; "all teams" offers everyone
  // across every team they run, deduplicated.
  const peopleInScope = (() => {
    const teams = teamId === 'all' ? managedTeams : managedTeams.filter((t) => t.id === teamId);
    const byId = new Map<string, { userId: string; name: string }>();
    teams.forEach((t) => t.members.forEach((m) => byId.set(m.userId, m)));
    return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name));
  })();

  // The search term is applied in the browser, so it must not be part of the
  // key: putting it there made every keystroke a brand-new query, which flipped
  // isLoading back to true, replaced the page with the spinner and tore the
  // search box out of the DOM, losing focus after each letter.
  const { data: tasksResponse, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['tasks', { status: statusFilter, scope, teamId, assigneeId, mineAs }],
    queryFn: () =>
      tasksApi.list({
        status: statusFilter !== 'all' ? statusFilter : undefined,
        // 'all' is everything in the organization; the server takes no scope for it.
        scope: scope === 'all' ? undefined : scope,
        as: scope === 'mine' && mineAs !== 'assigned' ? mineAs : undefined,
        teamId: scope === 'team' && teamId !== 'all' ? teamId : undefined,
        assigneeId: scope === 'team' && assigneeId !== 'all' ? assigneeId : undefined,
      }),
    placeholderData: keepPreviousData,
    ...liveTaskQuery,
  });

  const tasks = tasksResponse?.data || [];

  const updateTaskMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: any }) => tasksApi.update(id, data),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tasks'] }),
    onError: (err) => toast.error(apiErrorMessage(err, 'Could not update that task.')),
  });

  const filteredTasks = tasks.filter((task) => {
    if (searchQuery && !task.title.toLowerCase().includes(searchQuery.toLowerCase())) return false;
    if (statusFilter !== 'all' && task.status !== statusFilter) return false;
    if (labelFilter !== 'all' && !task.labels.some((l) => l.name === labelFilter)) return false;
    return true;
  });

  const countIn = (status: string) => filteredTasks.filter((t) => t.status === status).length;
  const onPhone = filteredTasks.filter((t) => t.status === phoneColumn);

  const teamFilters = (phone: boolean) => (
    <>
      {managedTeams.length > 1 && (
        <Select value={teamId} onValueChange={(v) => { setTeamId(v); setAssigneeId('all'); }}>
          <SelectTrigger className={phone ? 'h-9 min-w-0 flex-1' : 'h-9 w-40'} aria-label="Filter by team"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All teams</SelectItem>
            {managedTeams.map((t) => (
              <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      <Select value={assigneeId} onValueChange={setAssigneeId}>
        <SelectTrigger className={phone ? 'h-9 min-w-0 flex-1' : 'h-9 w-44'} aria-label="Filter by person"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Everyone</SelectItem>
          {peopleInScope.map((m) => (
            <SelectItem key={m.userId} value={m.userId}>{m.name}</SelectItem>
          ))}
        </SelectContent>
      </Select>
    </>
  );

  // Only once the organization has labels: an empty choice is a puzzle.
  const labelSelect = (phone: boolean) =>
    allLabels.length > 0 && (
      <Select value={labelFilter} onValueChange={setLabelFilter}>
        <SelectTrigger className={phone ? 'h-9 min-w-0 flex-1' : 'h-9 w-40'} aria-label="Filter by label"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="all">All labels</SelectItem>
          {allLabels.map((l) => (
            <SelectItem key={l.id} value={l.name}>
              <span className="flex items-center gap-2">
                <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: l.color }} aria-hidden="true" />
                {l.name}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    );

  const mineFilter = (phone: boolean) => (
    <Select value={mineAs} onValueChange={(v) => setMineAs(v as typeof mineAs)}>
      <SelectTrigger className={phone ? 'h-9 min-w-0 flex-1' : 'h-9 w-40'} aria-label="Which of my tasks"><SelectValue /></SelectTrigger>
      <SelectContent>
        <SelectItem value="assigned">Assigned to me</SelectItem>
        <SelectItem value="created">Created by me</SelectItem>
        <SelectItem value="added">Added to</SelectItem>
      </SelectContent>
    </Select>
  );

  return (
    // min-h-0 all the way down, or the scrolling region grows to fit its
    // contents instead of the screen and the whole page scrolls behind the
    // header. On a phone that is the difference between one screen and four.
    <div className="flex h-full min-h-0 flex-col">
      {/* Toolbar */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-2 md:px-6">
        <Tabs value={scope} onValueChange={(v) => setScope(v as typeof scope)} className="min-w-0 flex-1 md:flex-none">
          <TabsList className="h-9 w-full justify-start overflow-x-auto scrollbar-hide md:w-auto">
            <TabsTrigger value="mine" className="py-1 text-xs sm:text-sm">My tasks</TabsTrigger>
            {managesSomebody && <TabsTrigger value="team" className="py-1 text-xs sm:text-sm">Team tasks</TabsTrigger>}
            <TabsTrigger value="all" className="py-1 text-xs sm:text-sm">All tasks</TabsTrigger>
          </TabsList>
        </Tabs>

        {/* Phone: searching is an icon, so it costs no row. */}
        <Button
          variant="ghost"
          size="icon"
          className="shrink-0 md:hidden"
          aria-label="Search"
          aria-pressed={searchOpen}
          onClick={() => {
            setSearchOpen((open) => {
              if (open) setSearchQuery('');
              return !open;
            });
          }}
        >
          <Search className="h-5 w-5" />
        </Button>

        <div role="group" aria-label="Layout" className="hidden items-center rounded-md border border-border p-0.5 md:inline-flex">
          {(['list', 'board'] as const).map((mode) => (
            <Button
              key={mode}
              variant={viewMode === mode ? 'secondary' : 'ghost'}
              size="sm"
              className="h-7 px-2.5 capitalize"
              aria-pressed={viewMode === mode}
              onClick={() => setViewMode(mode)}
            >
              {mode}
            </Button>
          ))}
        </div>

        <div className="hidden flex-wrap items-center gap-2 md:ml-auto md:flex">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search tasks..."
              aria-label="Search tasks"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-9 w-56 pl-10"
            />
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Filter className="mr-2 h-4 w-4" />
                {columns.find((c) => c.id === statusFilter)?.title ?? 'All Status'}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => setStatusFilter('all')}>All Status</DropdownMenuItem>
              <DropdownMenuSeparator />
              {columns.map((col) => (
                <DropdownMenuItem key={col.id} onSelect={() => setStatusFilter(col.id)}>
                  {col.title}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          {labelSelect(false)}
          {scope === 'team' && teamFilters(false)}
          {scope === 'mine' && mineFilter(false)}
        </div>
      </div>

      {/* Phone: search appears only when asked for, and the team filters only
          when the chosen scope has any. Neither costs a row otherwise. */}
      {searchOpen && (
        <div className="flex shrink-0 flex-col gap-2 border-b border-border px-3 py-2 md:hidden">
          {labelSelect(true)}
          <Input
            autoFocus
            placeholder="Search tasks..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            aria-label="Search tasks"
          />
        </div>
      )}
      {scope === 'team' && (
        <div className="flex shrink-0 gap-2 border-b border-border px-3 py-2 md:hidden">{teamFilters(true)}</div>
      )}
      {scope === 'mine' && (
        <div className="flex shrink-0 gap-2 border-b border-border px-3 py-2 md:hidden">{mineFilter(true)}</div>
      )}

      {/* Phone: one column at a time, picked here. Columns side by side on a
          phone meant scrolling sideways to find a column and downwards to read
          it, with the counts of the others out of sight. */}
      <div className="flex shrink-0 gap-1 overflow-x-auto scrollbar-hide border-b border-border px-3 py-2 md:hidden">
        {columns.map((column) => (
          <button
            key={column.id}
            onClick={() => setPhoneColumn(column.id)}
            aria-pressed={phoneColumn === column.id}
            className={cn(
              'flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1.5 text-xs font-medium transition-colors',
              phoneColumn === column.id
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground'
            )}
          >
            {column.title}
            <span
              className={cn(
                'rounded-full px-1.5 text-[11px] tabular-nums',
                phoneColumn === column.id ? 'bg-primary-foreground/20' : 'bg-background/70'
              )}
            >
              {countIn(column.id)}
            </span>
          </button>
        ))}
      </div>

      {/* Task Board */}
      <div className="min-h-0 flex-1 overflow-y-auto p-3 md:overflow-auto md:p-6">
        {isLoading ? (
          <LoadingState label="Loading tasks" />
        ) : isError && !tasksResponse ? (
          <ErrorState error={error} fallback="Could not load tasks." onRetry={() => refetch()} />
        ) : (
          <>
            {/* Phone */}
            <div className="space-y-2 md:hidden">
              {onPhone.length === 0 ? (
                <p className="py-12 text-center text-sm text-muted-foreground">
                  Nothing in {columns.find((c) => c.id === phoneColumn)?.title.toLowerCase()}.
                </p>
              ) : (
                onPhone.map((task) => (
                  <TaskCard key={task.id} task={task} onUpdate={updateTaskMutation.mutate} />
                ))
              )}
            </div>

            {/* Anything wider */}
            {viewMode === 'board' ? (
              <div className="hidden h-full gap-4 overflow-x-auto pb-2 md:flex">
                {columns.map((column) => (
                  <div key={column.id} className="flex w-72 min-h-0 flex-shrink-0 flex-col">
                    <div className={cn('rounded-t-lg px-3 py-2 text-sm font-medium', column.head)}>
                      {column.title}
                      <span className="ml-2 text-xs tabular-nums opacity-70">{countIn(column.id)}</span>
                    </div>
                    <div className={cn('min-h-0 flex-1 overflow-y-auto rounded-b-lg p-2 space-y-2', column.body)}>
                      {filteredTasks.filter((t) => t.status === column.id).map((task) => (
                        <TaskCard key={task.id} task={task} onUpdate={updateTaskMutation.mutate} />
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            ) : filteredTasks.length === 0 ? (
              <p className="hidden py-12 text-center text-sm text-muted-foreground md:block">No tasks to show.</p>
            ) : (
              <div className="hidden space-y-4 md:block">
                {filteredTasks.map((task) => (
                  <TaskCard key={task.id} task={task} onUpdate={updateTaskMutation.mutate} />
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function TaskCard({ task, onUpdate }: { task: Task; onUpdate: (data: { id: string; data: any }) => void }) {
  // The footer used to be drawn whether or not it held anything, so a task with
  // nobody on it and no date ended in an empty bordered strip; on a phone that
  // is a row of the screen spent on nothing.
  const hasFooter = !!(task.assigneeName || task.dueDate || task.projectName);

  // A manager's team board deliberately shows work they cannot act on: an
  // organisation admin sitting on their team as an ordinary engineer, most
  // often. The card has to say so, or the first they learn of it is a 403.
  const restriction = restrictionNote(task.can);
  const due = dueLabel(task);

  return (
    <div
      className={cn(
        'rounded-lg border border-border bg-card p-3 shadow-sm transition-shadow hover:shadow-md md:p-4',
        restriction && 'border-dashed bg-muted/40'
      )}
    >
      <div className="mb-2 flex items-start justify-between gap-2">
        {/* The card had no way into the task at all, so the detail page
            (comments, history, members) was unreachable from the board. */}
        <UnreadTaskDot taskId={task.id} className="mt-1.5 shrink-0" />
        <Link
          to={`/tasks/${task.id}`}
          className="min-w-0 flex-1 break-anywhere text-sm font-medium hover:underline"
        >
          {task.key && <span className="mr-1.5 font-mono text-xs font-normal text-muted-foreground">{task.key}</span>}
          {task.title}
        </Link>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="h-6 w-6" aria-label="Task actions">
              <MoreVertical className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'todo' } })}>Move to To Do</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'in_progress' } })}>Move to In Progress</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'blocked' } })}>Mark Blocked</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'done' } })}>Mark Done</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link to={`/tasks/${task.id}`}>Open</Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {restriction && (
        <p className="mb-2 flex items-start gap-1.5 text-xs text-muted-foreground">
          <Lock className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
          <span className="break-anywhere">{restriction}</span>
        </p>
      )}

      {task.description && <p className="mb-2 line-clamp-2 break-anywhere text-sm text-muted-foreground md:mb-3">{task.description}</p>}

      <div className={cn('flex flex-wrap items-center gap-2', hasFooter ? 'mb-2 md:mb-3' : '')}>
        {/* The status is the column you are looking at, so on a phone repeating
            it on every card says nothing. */}
        <Badge variant="secondary" className={cn('hidden text-xs md:inline-flex', statusColors[task.status])}>
          {task.status.replace('_', ' ')}
        </Badge>
        <Badge variant="secondary" className={cn('text-xs', priorityColors[task.priority])}>
          {task.priority}
        </Badge>
        {task.taskType !== 'task' && (
          <Badge variant="outline" className="text-xs">{task.taskType}</Badge>
        )}
        {task.labels.map((label) => (
          <LabelBadge key={label.name} name={label.name} color={label.color} className="max-w-[10rem]" />
        ))}
      </div>

      {hasFooter && (
      <div className="flex items-center justify-between border-t border-border pt-2">
        <div className="flex items-center gap-2">
          {task.assigneeName && (
            <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
              <PersonAvatar userId={task.assigneeId} name={task.assigneeName} className="h-5 w-5" fallbackClassName="text-[9px]" decorative />
              <span className="truncate">{task.assigneeName}</span>
            </div>
          )}
          {due && (
            <div className={cn('flex items-center gap-1 text-xs text-muted-foreground', due.overdue && DANGER_TEXT)}>
              <Calendar className="h-3 w-3" aria-hidden />
              <span className="tabular-nums">{due.text}</span>
            </div>
          )}
        </div>
        {task.projectName && (
          <span className="text-xs text-muted-foreground">{task.projectName}</span>
        )}
      </div>
      )}
    </div>
  );
}
