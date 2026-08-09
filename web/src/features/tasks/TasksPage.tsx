import { useState } from 'react';
import { Search, Filter, MoreVertical, Calendar, User, Loader2 } from 'lucide-react';
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

  return (
    <div className="flex h-full flex-col bg-background">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2 md:h-16 md:flex-nowrap md:py-0 md:px-6">
        {/* On a phone the title and the tabs each get their own line; four tabs
            do not fit beside a heading. */}
        <div className="flex w-full min-w-0 flex-col gap-2 md:w-auto md:flex-row md:items-center md:gap-3">
          <h1 className="text-xl font-bold md:text-2xl">Tasks</h1>
          <Tabs value={scope} onValueChange={(v) => setScope(v as typeof scope)} className="min-w-0">
            <TabsList className="w-full justify-start overflow-x-auto md:w-auto">
              <TabsTrigger value="mine">My tasks</TabsTrigger>
              {managesSomebody && <TabsTrigger value="team">Team tasks</TabsTrigger>}
              {managesSomebody && <TabsTrigger value="standup">Standup</TabsTrigger>}
              <TabsTrigger value="all">All tasks</TabsTrigger>
            </TabsList>
          </Tabs>
          <div className="hidden sm:flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => setViewMode('list')}>
              <span className="h-4 w-4" /> List
            </Button>
            <Button variant="outline" size="sm" onClick={() => setViewMode('board')}>
              <span className="h-4 w-4" /> Board
            </Button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative hidden sm:block">
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

      {/* Task Board */}
      <div className="flex-1 overflow-auto p-4 md:p-6">
        {scope === 'standup' ? (
          <StandupView teamId={teamId} assigneeId={assigneeId} />
        ) : viewMode === 'board' ? (
          <div className="flex h-full min-h-[calc(100vh-220px)] snap-x snap-mandatory gap-4 overflow-x-auto pb-2">
            {columns.map((column) => (
              <div
                key={column.id}
                className="flex w-[85vw] max-w-xs flex-shrink-0 snap-start flex-col sm:w-72"
              >
                <div className={cn('rounded-t-lg px-3 py-2 text-sm font-medium', column.color)}>
                  {column.title}
                  <span className="ml-2 text-xs opacity-70">
                    {filteredTasks.filter((t) => t.status === column.id).length}
                  </span>
                </div>
                <div className={cn('flex-1 overflow-y-auto rounded-b-lg p-2 space-y-2', column.color + '/50')}>
                  {filteredTasks.filter((t) => t.status === column.id).map((task) => (
                    <TaskCard key={task.id} task={task} onUpdate={updateTaskMutation.mutate} />
                  ))}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="space-y-4">
            {filteredTasks.map((task) => (
              <TaskCard key={task.id} task={task} onUpdate={updateTaskMutation.mutate} />
            ))}
          </div>
        )}
      </div>

      <CreateTaskDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  );
}

function TaskCard({ task, onUpdate }: { task: Task; onUpdate: (data: { id: string; data: any }) => void }) {
  return (
    <div className="rounded-lg border border-border bg-card p-4 shadow-sm transition-shadow hover:shadow-md">
      <div className="mb-2 flex items-start justify-between gap-2">
        {/* The card had no way into the task at all, so the detail page —
            comments, history, members — was unreachable from the board. */}
        <Link
          to={`/tasks/${task.id}`}
          className="flex-1 text-sm font-medium hover:underline"
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

      {task.description && <p className="text-sm text-muted-foreground mb-3 line-clamp-2">{task.description}</p>}

      <div className="flex flex-wrap items-center gap-2 mb-3">
        <Badge variant="secondary" className={cn('text-xs', statusColors[task.status])}>
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

      <div className="flex items-center justify-between pt-2 border-t border-border">
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
    </div>
  );
}
