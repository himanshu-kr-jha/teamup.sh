import { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { ChevronLeft, Calendar, MessageSquare, Plus, MoreVertical } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Textarea } from '@/shared/components/ui/textarea';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Badge } from '@/shared/components/ui/badge';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { formatRelativeTime, formatDate } from '@/shared/utils/cn';
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
  reporterId?: string;
  reporterName?: string;
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

const statuses = ['todo', 'in_progress', 'in_review', 'done', 'blocked'] as const;
const priorities = ['low', 'medium', 'high', 'urgent'] as const;

export function TaskDetailPage() {
  const { taskId } = useParams<{ taskId: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [showCommentForm, setShowCommentForm] = useState(false);
  const [commentText, setCommentText] = useState('');

  const { data: taskResponse, isLoading } = useQuery({
    queryKey: ['task', taskId],
    queryFn: () => tasksApi.get(taskId!),
    enabled: !!taskId,
  });

  const task = taskResponse?.data;

  const updateTaskMutation = useMutation({
    mutationFn: (data: Partial<Task>) => tasksApi.update(taskId!, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      setEditing(false);
    },
  });

  const deleteTaskMutation = useMutation({
    mutationFn: () => tasksApi.delete(taskId!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      navigate('/tasks');
    },
  });

  const addCommentMutation = useMutation({
    mutationFn: (content: string) => tasksApi.addComment(taskId!, content),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
      setCommentText('');
      setShowCommentForm(false);
    },
  });

  if (isLoading) {
    return <div className="flex h-full items-center justify-center">Loading...</div>;
  }

  if (!task) {
    return <div className="flex h-full items-center justify-center">Task not found</div>;
  }

  const handleStatusChange = (status: typeof statuses[number]) => {
    updateTaskMutation.mutate({ status });
  };

  const handlePriorityChange = (priority: typeof priorities[number]) => {
    updateTaskMutation.mutate({ priority });
  };

  return (
    <div className="flex h-full flex-col bg-background">
      {/* Header */}
      <div className="flex h-16 items-center justify-between px-4 md:px-6 border-b border-border">
        <Button variant="ghost" size="icon" onClick={() => navigate(-1)}>
          <ChevronLeft className="h-5 w-5" />
        </Button>
        <div className="flex-1" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon">
              <MoreVertical className="h-5 w-5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => setEditing(true)}>Edit</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive" onSelect={() => deleteTaskMutation.mutate()}>Delete</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {isLoading ? (
        <div className="flex-1 items-center justify-center">Loading...</div>
      ) : (
        <div className="flex-1 overflow-auto p-4 md:p-6">
          <div className="max-w-4xl mx-auto space-y-6">
            {/* Main task info */}
            <div className="flex flex-col md:flex-row md:items-start gap-6">
              <div className="flex-1 space-y-4">
                <div className="flex items-start justify-between gap-4">
                  <div>
                    {editing ? (
                      <Input value={task.title} onChange={e => updateTaskMutation.mutate({ title: e.target.value })} className="text-2xl font-bold" autoFocus />
                    ) : (
                      <h1 className="text-2xl font-bold">{task.title}</h1>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    <Select value={task.status} onValueChange={handleStatusChange}>
                      <SelectTrigger className="w-[140px]">
                        <SelectValue placeholder="Status" />
                      </SelectTrigger>
                      <SelectContent>
                        {statuses.map(s => (
                          <SelectItem key={s} value={s}>{s.replace('_', ' ')}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Select value={task.priority} onValueChange={handlePriorityChange}>
                      <SelectTrigger className="w-[140px]">
                        <SelectValue placeholder="Priority" />
                      </SelectTrigger>
                      <SelectContent>
                        {priorities.map(p => (
                          <SelectItem key={p} value={p}>{p}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                {/* Description */}
                <div className="space-y-2">
                  <label className="text-sm font-medium">Description</label>
                  {editing ? (
                    <Textarea value={task.description} onChange={e => updateTaskMutation.mutate({ description: e.target.value })} rows={6} />
                  ) : (
                    <div className="prose prose-sm max-w-none">{task.description || <span className="text-muted-foreground">No description</span>}</div>
                  )}
                </div>

                {/* Metadata */}
                <div className="flex flex-wrap items-center gap-3 pt-4 border-t border-border">
                  <Badge variant="secondary" className={statusColors[task.status]}>
                    {task.status.replace('_', ' ')}
                  </Badge>
                  <Badge variant="secondary" className={priorityColors[task.priority]}>
                    {task.priority}
                  </Badge>
                  <Badge variant="outline">{task.taskType}</Badge>
                  {task.labels.map(label => (
                    <Badge key={label.name} variant="secondary" style={{ backgroundColor: label.color }}>
                      {label.name}
                    </Badge>
                  ))}
                </div>
              </div>

              {/* Sidebar */}
              <div className="w-full md:w-80 space-y-6">
                {/* Assignee */}
                <Card>
                  <CardHeader>
                    <CardTitle>Assignee</CardTitle>
                  </CardHeader>
                  <CardContent>
                    {task.assigneeName ? (
                      <div className="flex items-center gap-3">
                        <div className="h-10 w-10 rounded-full bg-primary/10 flex items-center justify-center">
                          <span className="text-lg font-medium text-primary">{task.assigneeName.charAt(0)}</span>
                        </div>
                        <div>
                          <p className="font-medium">{task.assigneeName}</p>
                          <p className="text-sm text-muted-foreground">Assignee</p>
                        </div>
                      </div>
                    ) : (
                      <Button variant="outline" className="w-full" onClick={() => {}}>
                        <Plus className="mr-2 h-4 w-4" />
                        Assign
                      </Button>
                    )}
                  </CardContent>
                </Card>

                {/* Due Date */}
                <Card>
                  <CardHeader>
                    <CardTitle>Due Date</CardTitle>
                  </CardHeader>
                  <CardContent>
                    {task.dueDate ? (
                      <div className="flex items-center gap-2 text-sm">
                        <Calendar className="h-4 w-4 text-muted-foreground" />
                        <span>{formatDate(task.dueDate)}</span>
                      </div>
                    ) : (
                      <Button variant="outline" className="w-full" onClick={() => {}}>
                        <Calendar className="mr-2 h-4 w-4" />
                        Set due date
                      </Button>
                    )}
                  </CardContent>
                </Card>

                {/* Reporter */}
                <Card>
                  <CardHeader>
                    <CardTitle>Reporter</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <div className="flex items-center gap-3">
                      <div className="h-8 w-8 rounded-full bg-secondary flex items-center justify-center">
                        <span className="text-sm font-medium">{task.reporterName?.charAt(0)}</span>
                      </div>
                      <div>
                        <p className="font-medium">{task.reporterName}</p>
                        <p className="text-sm text-muted-foreground">Created {formatRelativeTime(task.createdAt)}</p>
                      </div>
                    </div>
                  </CardContent>
                </Card>

                {/* Project */}
                {task.projectName && (
                  <Card>
                    <CardHeader>
                      <CardTitle>Project</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <p className="font-medium">{task.projectName}</p>
                    </CardContent>
                  </Card>
                )}
              </div>
            </div>

            {/* Comments */}
            <Card>
              <CardHeader className="flex flex-row items-center justify-between">
                <CardTitle>Comments</CardTitle>
                <Button variant="ghost" size="sm" onClick={() => setShowCommentForm(true)}>
                  <MessageSquare className="mr-2 h-4 w-4" />
                  Add Comment
                </Button>
              </CardHeader>
              <CardContent>
                {showCommentForm && (
                  <form onSubmit={e => { e.preventDefault(); if (commentText.trim()) { addCommentMutation.mutate(commentText); } }} className="space-y-3 mb-4">
                    <Textarea value={commentText} onChange={e => setCommentText(e.target.value)} placeholder="Write a comment..." rows={3} />
                    <div className="flex justify-end gap-2">
                      <Button variant="outline" type="button" onClick={() => setShowCommentForm(false)}>Cancel</Button>
                      <Button type="submit" disabled={!commentText.trim() || addCommentMutation.isPending}>
                        {addCommentMutation.isPending ? 'Posting...' : 'Post Comment'}
                      </Button>
                    </div>
                  </form>
                )}

                <div className="space-y-4">
                  {/* Comments would be fetched here */}
                  <p className="text-muted-foreground text-center py-8">No comments yet. Be the first to comment!</p>
                </div>
              </CardContent>
            </Card>

            {/* Activity */}
            <Card>
              <CardHeader>
                <CardTitle>Activity</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-muted-foreground text-center py-8">Activity log coming soon...</p>
              </CardContent>
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}
