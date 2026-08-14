import { useState, useEffect, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { ChevronLeft, Calendar, MessageSquare, MoreVertical, UserPlus, History, Paperclip } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Textarea } from '@/shared/components/ui/textarea';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Badge } from '@/shared/components/ui/badge';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { formatRelativeTime, formatDate } from '@/shared/utils/cn';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { tasksApi, filesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { Label } from '@/shared/components/ui/label';
import { FormDialog } from '@/shared/components/FormDialog';
import { useAuth } from '@/features/auth/AuthProvider';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { AttachmentChip } from '@/features/files/AttachmentChip';
import { useUpload, type UploadedFile } from '@/features/files/useUpload';
import { cn } from '@/shared/utils/cn';
import { restrictionNote, type TaskAbilities } from './abilities';

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

const statuses = ['todo', 'in_progress', 'in_review', 'done', 'blocked'] as const;
const priorities = ['low', 'medium', 'high', 'urgent'] as const;

export function TaskDetailPage() {
  const { taskId } = useParams<{ taskId: string }>();
  // Only the people the org chart will actually accept. Offering everybody in
  // the organisation meant the picker could hand back a name the PATCH refuses.
  const { data: membersData } = useQuery({
    queryKey: ['assignable-users'],
    queryFn: () => tasksApi.assignableUsers(),
  });
  const members = membersData?.data ?? [];
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const toast = useToast();
  const { on } = useWebSocket();
  const [editing, setEditing] = useState(false);
  const [showCommentForm, setShowCommentForm] = useState(false);
  const [commentText, setCommentText] = useState('');
  // Uploaded but not yet posted, so a file arrives with the words that explain
  // it rather than on its own the moment it finishes uploading.
  const [staged, setStaged] = useState<UploadedFile[]>([]);
  const commentFileRef = useRef<HTMLInputElement>(null);
  const { upload, progress: uploadProgress, error: uploadError } = useUpload('tasks', taskId ?? '');

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
    mutationFn: ({ content, attachmentIds }: { content: string; attachmentIds: string[] }) =>
      tasksApi.addComment(taskId!, content, attachmentIds),
    onSuccess: () => {
      // The comment list is its own query; invalidating only the task left the
      // new comment invisible, so posting looked like it had failed.
      queryClient.invalidateQueries({ queryKey: ['task-comments', taskId] });
      queryClient.invalidateQueries({ queryKey: ['task-activities', taskId] });
      setCommentText('');
      setStaged([]);
      setShowCommentForm(false);
    },
    onError: (err) => toast.error(apiErrorMessage(err, 'Could not post that comment.')),
  });

  // A comment somebody else leaves appears where you are, rather than waiting
  // for you to reload the page you are already reading.
  useEffect(() => {
    return on('task.comment', (msg) => {
      if ((msg.payload as any)?.taskId !== taskId) return;
      queryClient.invalidateQueries({ queryKey: ['task-comments', taskId] });
      queryClient.invalidateQueries({ queryKey: ['task-activities', taskId] });
    });
  }, [on, taskId, queryClient]);

  useEffect(() => {
    if (uploadError) toast.error(uploadError);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uploadError]);

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

  // The board already told the caller what they may do here; the detail page
  // has to draw the same answer rather than offering a Delete that 403s.
  // Absent means an older server that did not say, and the old behaviour —
  // offer it and let the API decide — is the safe fallback.
  const mayDelete = task.can?.delete !== false;
  const mayReassign = task.can?.reassign !== false;
  const restriction = restrictionNote(task.can);

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
            {mayDelete ? (
              <DropdownMenuItem className="text-destructive" onSelect={() => deleteTaskMutation.mutate()}>Delete</DropdownMenuItem>
            ) : (
              <DropdownMenuItem disabled>
                Delete — {task.can?.reason ?? 'not yours to remove'}
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {isLoading ? (
        <div className="flex-1 items-center justify-center">Loading...</div>
      ) : (
        <div className="flex-1 overflow-auto p-4 md:p-6">
          <div className="max-w-4xl mx-auto space-y-6">
            {/* Main task info */}
            <div className="flex min-w-0 flex-col gap-6 md:flex-row md:items-start">
              <div className="min-w-0 flex-1 space-y-4">
                <div className="flex min-w-0 items-start justify-between gap-4">
                  <div className="min-w-0 flex-1">
                    {editing ? (
                      <Input
                        defaultValue={task.title}
                        className="text-2xl font-bold"
                        autoFocus
                        // Commit on blur or Enter: firing per keystroke saved
                        // the first character and closed the editor mid-word.
                        onBlur={(e) => {
                          const value = e.target.value.trim();
                          if (value && value !== task.title) updateTaskMutation.mutate({ title: value });
                          else setEditing(false);
                        }}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') e.currentTarget.blur();
                          if (e.key === 'Escape') setEditing(false);
                        }}
                      />
                    ) : (
                      <h1 className="break-anywhere text-2xl font-bold">{task.title}</h1>
                    )}
                    {restriction && (
                      <p className="mt-2 rounded-md border border-dashed border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
                        {restriction}
                      </p>
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
                    <Textarea
                      defaultValue={task.description}
                      rows={6}
                      onBlur={(e) => {
                        if (e.target.value !== task.description) {
                          updateTaskMutation.mutate({ description: e.target.value });
                        } else {
                          setEditing(false);
                        }
                      }}
                    />
                  ) : (
                    <div className="prose prose-sm max-w-none whitespace-pre-wrap break-anywhere">
                      {task.description || <span className="text-muted-foreground">No description</span>}
                    </div>
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
                  <CardContent className="space-y-3">
                    {task.assigneeName && (
                      <div className="flex items-center gap-3">
                        <div className="h-10 w-10 rounded-full bg-primary/10 flex items-center justify-center">
                          <span className="text-lg font-medium text-primary">{task.assigneeName.charAt(0)}</span>
                        </div>
                        <div>
                          <p className="font-medium">{task.assigneeName}</p>
                          <p className="text-sm text-muted-foreground">Assignee</p>
                        </div>
                      </div>
                    )}
                    {/* Moving work off somebody needs authority over them, so a
                        task held by an organisation admin shows who has it and
                        why it cannot be moved, rather than a picker that 403s. */}
                    {mayReassign ? (
                      <Select
                        value=""
                        onValueChange={(v) => updateTaskMutation.mutate({ assigneeId: v } as any)}
                      >
                        <SelectTrigger>
                          <SelectValue placeholder={task.assigneeName ? 'Reassign' : 'Assign someone'} />
                        </SelectTrigger>
                        <SelectContent>
                          {members.map((m: any) => (
                            <SelectItem key={m.userId} value={m.userId}>{m.name}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    ) : (
                      <p className="text-sm text-muted-foreground">
                        {task.can?.reason
                          ? `Cannot be reassigned — ${task.can.reason}.`
                          : 'Cannot be reassigned.'}
                      </p>
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
                      <Input
                        type="date"
                        aria-label="Set due date"
                        onChange={(e) =>
                          e.target.value &&
                          updateTaskMutation.mutate({
                            dueDate: new Date(`${e.target.value}T00:00:00`).toISOString(),
                          } as any)
                        }
                      />
                    )}
                  </CardContent>
                </Card>

                <LabelsCard taskId={taskId!} labels={task.labels ?? []} />
                <WatchersCard taskId={taskId!} />

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
                  <form
                    onSubmit={(e) => {
                      e.preventDefault();
                      if (!commentText.trim() && staged.length === 0) return;
                      addCommentMutation.mutate({
                        content: commentText,
                        attachmentIds: staged.map((f) => f.id),
                      });
                    }}
                    className="mb-4 space-y-3"
                  >
                    <Textarea
                      value={commentText}
                      onChange={(e) => setCommentText(e.target.value)}
                      placeholder="Write a comment…"
                      rows={3}
                    />

                    {staged.length > 0 && (
                      <div className="flex flex-wrap items-start gap-2">
                        {staged.map((file) => (
                          <span key={file.id} className="flex items-start gap-1">
                            <AttachmentChip attachment={{ ...file, sizeBytes: file.size }} />
                            <button
                              type="button"
                              aria-label={`Remove ${file.filename}`}
                              className="text-muted-foreground hover:text-foreground"
                              onClick={() => {
                                setStaged((all) => all.filter((f) => f.id !== file.id));
                                filesApi.discard(file.id).catch(() => {
                                  // The sweep collects it either way.
                                });
                              }}
                            >
                              ×
                            </button>
                          </span>
                        ))}
                      </div>
                    )}

                    <input
                      ref={commentFileRef}
                      type="file"
                      className="hidden"
                      aria-label="Attach a file to this comment"
                      onChange={async (e) => {
                        const file = e.target.files?.[0];
                        e.target.value = '';
                        if (!file) return;
                        const uploaded = await upload(file);
                        if (uploaded) setStaged((all) => [...all, uploaded]);
                      }}
                    />

                    <div className="flex flex-wrap justify-end gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        className="mr-auto"
                        disabled={uploadProgress !== null}
                        onClick={() => commentFileRef.current?.click()}
                      >
                        {uploadProgress !== null ? (
                          <span className="text-xs font-medium">{uploadProgress}%</span>
                        ) : (
                          <>
                            <Paperclip className="mr-2 h-4 w-4" />
                            Attach
                          </>
                        )}
                      </Button>
                      <Button variant="outline" type="button" onClick={() => setShowCommentForm(false)}>
                        Cancel
                      </Button>
                      <Button
                        type="submit"
                        disabled={
                          (!commentText.trim() && staged.length === 0) || addCommentMutation.isPending
                        }
                      >
                        {addCommentMutation.isPending ? 'Posting…' : 'Post comment'}
                      </Button>
                    </div>
                  </form>
                )}

                <CommentList taskId={taskId!} />
              </CardContent>
            </Card>

            {/* Activity */}
            <Card>
              <CardHeader>
                <CardTitle>Activity</CardTitle>
              </CardHeader>
              <CardContent>
                <ActivityList taskId={taskId!} />
              </CardContent>
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}


// Labels and watchers had no UI at all; watchers had no server route either.
function LabelsCard({ taskId, labels }: { taskId: string; labels: any[] }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data } = useQuery({ queryKey: ['labels'], queryFn: () => tasksApi.listLabels() });
  const all = data?.data ?? [];

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['task', taskId] });

  const attach = async (labelId: string) => {
    try {
      await tasksApi.addLabel(taskId, labelId);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not add that label.'));
    }
  };

  const detach = async (labelId: string) => {
    try {
      await tasksApi.removeLabel(taskId, labelId);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not remove that label.'));
    }
  };

  const available = all.filter((l: any) => !labels.some((x) => x.id === l.id));

  return (
    <Card>
      <CardHeader><CardTitle>Labels</CardTitle></CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap gap-1">
          {labels.length === 0 && <span className="text-sm text-muted-foreground">None yet.</span>}
          {labels.map((l: any) => (
            <Badge key={l.id} variant="secondary" className="gap-1" style={{ borderColor: l.color }}>
              {l.name}
              <button onClick={() => detach(l.id)} aria-label={`Remove ${l.name}`} className="opacity-60 hover:opacity-100">
                ×
              </button>
            </Badge>
          ))}
        </div>
        {available.length > 0 && (
          <Select value="" onValueChange={attach}>
            <SelectTrigger><SelectValue placeholder="Add a label" /></SelectTrigger>
            <SelectContent>
              {available.map((l: any) => (
                <SelectItem key={l.id} value={l.id}>{l.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </CardContent>
    </Card>
  );
}

/** Comments were posted but never displayed, so the page always read
 *  "No comments yet" no matter how many you left. */
function CommentList({ taskId }: { taskId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ['task-comments', taskId],
    queryFn: () => tasksApi.listComments(taskId),
  });
  const comments = data?.data ?? [];

  if (isLoading) return <p className="py-4 text-sm text-muted-foreground">Loading comments…</p>;
  if (comments.length === 0) {
    return <p className="py-8 text-center text-muted-foreground">No comments yet. Be the first to comment!</p>;
  }

  return (
    <ul className="space-y-4">
      {comments.map((comment: any) => (
        <li key={comment.id} className="flex gap-3">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10">
            <span className="text-sm font-medium text-primary">
              {(comment.userName || '?').charAt(0).toUpperCase()}
            </span>
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm">
              <span className="font-medium">{comment.userName || 'Someone'}</span>{' '}
              <span className="text-muted-foreground">{formatRelativeTime(comment.createdAt)}</span>
            </p>
            {comment.content && (
              <p className="whitespace-pre-wrap break-anywhere text-sm">{comment.content}</p>
            )}
            {comment.attachments?.length > 0 && (
              <div className={cn('flex flex-wrap gap-2', comment.content && 'mt-2')}>
                {comment.attachments.map((a: any) => (
                  <AttachmentChip key={a.id} attachment={a} />
                ))}
              </div>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}

/** The task's history. Every field change is already recorded server-side;
 *  the page just never asked for it. */
function ActivityList({ taskId }: { taskId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ['task-activities', taskId],
    queryFn: () => tasksApi.getActivities(taskId),
  });
  const activities = data?.data ?? [];

  if (isLoading) return <p className="py-4 text-sm text-muted-foreground">Loading history…</p>;
  if (activities.length === 0) {
    return <p className="py-8 text-center text-muted-foreground">Nothing has changed yet.</p>;
  }

  return (
    <ul className="space-y-3">
      {activities.map((a: any) => (
        <li key={a.id} className="flex items-start gap-3 text-sm">
          <History className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="break-anywhere">
              <span className="font-medium">{a.userName || 'Someone'}</span> changed{' '}
              <span className="font-medium">{a.field}</span>
              {a.oldValue ? <> from <span className="text-muted-foreground">{shorten(a.oldValue)}</span></> : null}
              {a.newValue ? <> to <span className="text-muted-foreground">{shorten(a.newValue)}</span></> : null}
            </p>
            <p className="text-xs text-muted-foreground">{formatRelativeTime(a.createdAt)}</p>
          </div>
        </li>
      ))}
    </ul>
  );
}

/**
 * As much of a changed value as belongs in a one-line history entry.
 *
 * The whole value was printed, so changing a comment put the entire comment in
 * the history twice — once as it was and once as it now is. Long enough to
 * recognise which change this was, short enough to stay one line.
 */
function shorten(value: string): string {
  const flat = String(value).replace(/\s+/g, ' ').trim();
  return flat.length > 60 ? flat.slice(0, 60) + '…' : flat;
}

/** Who is on this task. Anybody can put themselves on it; putting somebody
 *  else on it follows the same org chart as assigning them work. */
function WatchersCard({ taskId }: { taskId: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [adding, setAdding] = useState(false);
  const [pickedId, setPickedId] = useState('');

  const { data } = useQuery({
    queryKey: ['task-watchers', taskId],
    queryFn: () => tasksApi.listWatchers(taskId),
  });
  // Putting somebody else on a task follows the same org chart as assigning
  // them work, so the picker offers the same list — the whole organisation
  // included people the POST would refuse.
  const { data: usersData } = useQuery({
    queryKey: ['assignable-users'],
    queryFn: () => tasksApi.assignableUsers(),
  });

  const watchers = data?.data ?? [];
  const addable = usersData?.data ?? [];
  const following = watchers.some((w: any) => w.userId === user?.id);
  const candidates = addable.filter((u: any) => !watchers.some((w: any) => w.userId === u.userId));

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['task-watchers', taskId] });

  const toggle = async () => {
    try {
      if (following) await tasksApi.removeWatcher(taskId, user!.id);
      else await tasksApi.addWatcher(taskId, user!.id);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not change what you follow.'));
    }
  };

  const remove = async (userId: string) => {
    try {
      await tasksApi.removeWatcher(taskId, userId);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not remove them.'));
    }
  };

  return (
    <>
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0">
          <CardTitle>Members</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => setAdding(true)}>
            <UserPlus className="mr-2 h-4 w-4" />
            Add
          </Button>
        </CardHeader>
        <CardContent className="space-y-3">
          {watchers.length === 0 && <p className="text-sm text-muted-foreground">Nobody is on this task yet.</p>}
          <ul className="space-y-1">
            {watchers.map((w: any) => (
              <li key={w.userId} className="flex items-center justify-between gap-2 text-sm">
                <span className="truncate">{w.name}</span>
                <button
                  onClick={() => remove(w.userId)}
                  aria-label={`Remove ${w.name}`}
                  className="shrink-0 text-muted-foreground hover:text-foreground"
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
          <Button variant="outline" size="sm" className="w-full" onClick={toggle}>
            {following ? 'Stop following' : 'Follow this task'}
          </Button>
        </CardContent>
      </Card>

      <FormDialog
        open={adding}
        onOpenChange={(o) => {
          if (!o) setPickedId('');
          setAdding(o);
        }}
        title="Add somebody to this task"
        description="They will see it in their notifications and follow every change."
        submitLabel="Add"
        disabledReason={!pickedId ? 'Choose somebody to add.' : undefined}
        onSubmit={async () => {
          await tasksApi.addWatcher(taskId, pickedId);
          setPickedId('');
          refresh();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="taskMember">Person</Label>
          <Select value={pickedId} onValueChange={setPickedId}>
            <SelectTrigger id="taskMember"><SelectValue placeholder="Choose somebody" /></SelectTrigger>
            <SelectContent>
              {candidates.map((u: any) => (
                <SelectItem key={u.userId} value={u.userId}>{u.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </FormDialog>
    </>
  );
}
