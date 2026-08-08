import { useState } from 'react';
import { Search, Filter, MoreVertical, Calendar, User, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Badge } from '@/shared/components/ui/badge';
import { cn } from '@/shared/utils/cn';
import { formatRelativeTime } from '@/shared/utils/cn';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { tasksApi } from '@/shared/api/client';

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

  const { data: tasksResponse, isLoading } = useQuery({
    queryKey: ['tasks', { search: searchQuery, status: statusFilter }],
    queryFn: () => tasksApi.list({ status: statusFilter !== 'all' ? statusFilter : undefined }),
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
    return <div className="flex h-screen items-center justify-center"><Loader2 className="h-8 w-8 animate-spin" /></div>;
  }

  return (
    <div className="flex h-screen flex-col bg-background">
      {/* Header */}
      <div className="flex h-16 items-center justify-between px-4 md:px-6 border-b border-border">
        <div className="flex items-center gap-4">
          <h1 className="text-2xl font-bold">Tasks</h1>
          <div className="hidden sm:flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => setViewMode('list')}>
              <span className="h-4 w-4" /> List
            </Button>
            <Button variant="outline" size="sm" onClick={() => setViewMode('board')}>
              <span className="h-4 w-4" /> Board
            </Button>
          </div>
        </div>
        <div className="flex items-center gap-4">
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
          <Button onClick={() => {}}>New Task</Button>
        </div>
      </div>

      {/* Task Board */}
      <div className="flex-1 overflow-auto p-4 md:p-6">
        {viewMode === 'board' ? (
          <div className="flex gap-4 h-full min-h-[calc(100vh-200px)]">
            {columns.map((column) => (
              <div key={column.id} className="flex flex-col w-72 flex-shrink-0">
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
    </div>
  );
}

function TaskCard({ task, onUpdate }: { task: Task; onUpdate: (data: { id: string; data: any }) => void }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg border border-border p-4 shadow-sm hover:shadow-md transition-shadow">
      <div className="flex items-start justify-between gap-2 mb-2">
        <h3 className="font-medium text-sm flex-1">{task.title}</h3>
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
            <DropdownMenuItem className="text-destructive" onSelect={() => {}}>Delete</DropdownMenuItem>
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
