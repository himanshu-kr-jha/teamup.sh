import { useState } from 'react';
import { Search, Filter, MoreVertical, Calendar, User, Loader2, Plus } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Badge } from '@/shared/components/ui/badge';
import { cn } from '@/shared/utils/cn';
import { formatRelativeTime } from '@/shared/utils/cn';
import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs';
import { StandupView } from './StandupView';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { tasksApi } from '@/shared/api/client';
import { CreateTaskDialog } from './CreateTaskDialog';

interface Task {
  id: string;
  title: string;
  description: string;
  status: 'todo' | 'in_progress' | 'in_review' | 'done' | 'blocked';
  priority: 'low' | 'medium' | 'high' | 'urgent';
  taskType: 'task' | 'bug' | 'story' | 'epic' | 'subtask';
  assigneeId?: string;
  assigneeName?: string;
  assigneeAvatar?: string;
  dueDate?: string;
  labels: { name: string; color: string }[];
  projectId?: string;
  projectName?: string;
  createdAt: string;
  updatedAt: string;
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

export function TasksPage() {
  const queryClient = useQueryClient();
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [viewMode, setViewMode] = useState<'list' | 'board'>('board');
  const [createOpen, setCreateOpen] = useState(false);
  const [scope, setScope] = useState<'mine' | 'team' | 'all' | 'standup'>('mine');
  const [teamId, setTeamId] = useState('all');
  const [assigneeId, setAssigneeId] = useState('all');
  // Which column a phone is showing. Separate from the status filter, which is
  // the wide screen's way of narrowing a board that shows every column at once.
  const [phoneColumn, setPhoneColumn] = useState('todo');
  const [searchOpen, setSearchOpen] = useState(false);

  // Only somebody who actually manages a team gets the team view.
  const { data: managedData } = useQuery({
    queryKey: ['managed-teams'],
    queryFn: () => tasksApi.managedTeams(),
  });
  const managedTeams = managedData?.data ?? [];
  const managesSomebody = managedTeams.length > 0;

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
  // search box out of the DOM — losing focus after each letter.
  const { data: tasksResponse, isLoading } = useQuery({
    queryKey: ['tasks', { status: statusFilter, scope, teamId, assigneeId }],
    queryFn: () =>
      tasksApi.list({
        status: statusFilter !== 'all' ? statusFilter : undefined,
        // 'all' is everything in the organization, which is what the board
        // showed before the subtabs existed; the server takes no scope for it.
        // Standup fetches its own, grouped by person.
        scope: scope === 'all' || scope === 'standup' ? undefined : scope,
        teamId: scope === 'team' && teamId !== 'all' ? teamId : undefined,
        assigneeId: scope === 'team' && assigneeId !== 'all' ? assigneeId : undefined,
      }),
    placeholderData: keepPreviousData,
  });

  const tasks = tasksResponse?.data || [];


  const updateTaskMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: any }) => tasksApi.update(id, data),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tasks'] }),
  });

  const columns = [
    { id: 'todo', title: 'To Do', color: 'bg-gray-100' },
    { id: 'in_progress', title: 'In Progress', color: 'bg-blue-100' },
    { id: 'in_review', title: 'In Review', color: 'bg-purple-100' },
    { id: 'done', title: 'Done', color: 'bg-green-100' },
  ];

  const filteredTasks = tasks.filter((task) => {
    if (searchQuery && !task.title.toLowerCase().includes(searchQuery.toLowerCase())) return false;
    if (statusFilter !== 'all' && task.status !== statusFilter) return false;
    return true;
  });

  if (isLoading) {
    return <div className="flex h-full items-center justify-center"><Loader2 className="h-8 w-8 animate-spin" /></div>;
  }

  const countIn = (status: string) => filteredTasks.filter((t) => t.status === status).length;
  const onPhone = filteredTasks.filter((t) => t.status === phoneColumn);

  return (
    // min-h-0 all the way down, or the scrolling region grows to fit its
    // contents instead of the screen and the whole page scrolls behind the
    // header. On a phone that is the difference between one screen and four.
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Header */}
      <div className="shrink-0 border-b border-border px-3 py-2 md:flex md:h-16 md:items-center md:justify-between md:gap-2 md:px-6 md:py-0">
        <div className="flex min-w-0 items-center gap-2 md:gap-3">
          {/* The heading goes on a phone: the navigation already says where you
              are, and the row is worth more as tabs than as a label. */}
          <h1 className="hidden text-xl font-bold md:block md:text-2xl">Tasks</h1>
          <Tabs value={scope} onValueChange={(v) => setScope(v as typeof scope)} className="min-w-0 flex-1 md:flex-none">
            <TabsList className="w-full justify-start overflow-x-auto md:w-auto">
              <TabsTrigger value="mine">My tasks</TabsTrigger>
              {managesSomebody && <TabsTrigger value="team">Team tasks</TabsTrigger>}
              {managesSomebody && <TabsTrigger value="standup">Standup</TabsTrigger>}
              <TabsTrigger value="all">All tasks</TabsTrigger>
            </TabsList>
          </Tabs>
          <div className="hidden md:flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => setViewMode('list')}>
              <span className="h-4 w-4" /> List
            </Button>
            <Button variant="outline" size="sm" onClick={() => setViewMode('board')}>
              <span className="h-4 w-4" /> Board
            </Button>
          </div>

          {/* Phone: searching and adding are icons, so they cost no row. */}
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
          <Button size="icon" className="shrink-0 md:hidden" aria-label="New task" onClick={() => setCreateOpen(true)}>
            <Plus className="h-5 w-5" />
          </Button>
        </div>

        <div className="mt-2 hidden flex-wrap items-center gap-2 md:mt-0 md:flex">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search tasks..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="pl-10 w-64"
            />
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Filter className="mr-2 h-4 w-4" />
                {statusFilter === 'all' ? 'All Status' : statusFilter.replace('_', ' ')}
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
          {(scope === 'team' || scope === 'standup') && managedTeams.length > 1 && (
            <Select value={teamId} onValueChange={(v) => { setTeamId(v); setAssigneeId('all'); }}>
              <SelectTrigger className="w-40"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All teams</SelectItem>
                {managedTeams.map((t) => (
                  <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {(scope === 'team' || scope === 'standup') && (
            <Select value={assigneeId} onValueChange={setAssigneeId}>
              <SelectTrigger className="w-44"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">Everyone</SelectItem>
                {peopleInScope.map((m) => (
                  <SelectItem key={m.userId} value={m.userId}>{m.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Button onClick={() => setCreateOpen(true)}>New Task</Button>
        </div>
      </div>

      {/* Phone: search appears only when asked for, and the team filters only
          when the chosen scope has any. Neither costs a row otherwise. */}
      {searchOpen && (
        <div className="shrink-0 border-b border-border px-3 py-2 md:hidden">
          <Input
            autoFocus
            placeholder="Search tasks..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            aria-label="Search tasks"
          />
        </div>
      )}
      {(scope === 'team' || scope === 'standup') && (
        <div className="flex shrink-0 gap-2 border-b border-border px-3 py-2 md:hidden">
          {managedTeams.length > 1 && (
            <Select value={teamId} onValueChange={(v) => { setTeamId(v); setAssigneeId('all'); }}>
              <SelectTrigger className="h-9 min-w-0 flex-1"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All teams</SelectItem>
                {managedTeams.map((t) => (
                  <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Select value={assigneeId} onValueChange={setAssigneeId}>
            <SelectTrigger className="h-9 min-w-0 flex-1"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Everyone</SelectItem>
              {peopleInScope.map((m) => (
                <SelectItem key={m.userId} value={m.userId}>{m.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {/* Phone: one column at a time, picked here. Four columns side by side on
          a phone meant scrolling sideways to find a column and downwards to
          read it, with the counts of the other three out of sight. */}
      {scope !== 'standup' && (
        <div className="flex shrink-0 gap-1 overflow-x-auto border-b border-border px-3 py-2 md:hidden">
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
                  'rounded-full px-1.5 text-[11px]',
                  phoneColumn === column.id ? 'bg-primary-foreground/20' : 'bg-background/70'
                )}
              >
                {countIn(column.id)}
              </span>
            </button>
          ))}
        </div>
      )}

      {/* Task Board */}
      <div className="min-h-0 flex-1 overflow-y-auto p-3 md:overflow-auto md:p-6">
        {scope === 'standup' ? (
          <StandupView teamId={teamId} assigneeId={assigneeId} />
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
                    <div className={cn('rounded-t-lg px-3 py-2 text-sm font-medium', column.color)}>
                      {column.title}
                      <span className="ml-2 text-xs opacity-70">{countIn(column.id)}</span>
                    </div>
                    <div className={cn('min-h-0 flex-1 overflow-y-auto rounded-b-lg p-2 space-y-2', column.color + '/50')}>
                      {filteredTasks.filter((t) => t.status === column.id).map((task) => (
                        <TaskCard key={task.id} task={task} onUpdate={updateTaskMutation.mutate} />
                      ))}
                    </div>
                  </div>
                ))}
              </div>
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

      <CreateTaskDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  );
}

function TaskCard({ task, onUpdate }: { task: Task; onUpdate: (data: { id: string; data: any }) => void }) {
  // The footer used to be drawn whether or not it held anything, so a task with
  // nobody on it and no date ended in an empty bordered strip — on a phone that
  // is a row of the screen spent on nothing.
  const hasFooter = !!(task.assigneeName || task.dueDate || task.projectName);

  return (
    <div className="rounded-lg border border-border bg-card p-3 shadow-sm transition-shadow hover:shadow-md md:p-4">
      <div className="mb-2 flex items-start justify-between gap-2">
        {/* The card had no way into the task at all, so the detail page —
            comments, history, members — was unreachable from the board. */}
        <Link
          to={`/tasks/${task.id}`}
          className="min-w-0 flex-1 break-anywhere text-sm font-medium hover:underline"
        >
          {task.title}
        </Link>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="h-6 w-6">
              <MoreVertical className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'todo' } })}>Move to To Do</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'in_progress' } })}>Move to In Progress</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onUpdate({ id: task.id, data: { status: 'done' } })}>Mark Done</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link to={`/tasks/${task.id}`}>Open</Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

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
          <Badge key={label.name} variant="secondary" className="text-xs" style={{ backgroundColor: label.color }}>
            {label.name}
          </Badge>
        ))}
      </div>

      {hasFooter && (
      <div className="flex items-center justify-between border-t border-border pt-2">
        <div className="flex items-center gap-2">
          {task.assigneeName && (
            <div className="flex items-center gap-1 text-xs text-muted-foreground">
              <User className="h-3 w-3" />
              <span>{task.assigneeName}</span>
            </div>
          )}
          {task.dueDate && (
            <div className="flex items-center gap-1 text-xs text-muted-foreground">
              <Calendar className="h-3 w-3" />
              <span>{formatRelativeTime(task.dueDate)}</span>
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
