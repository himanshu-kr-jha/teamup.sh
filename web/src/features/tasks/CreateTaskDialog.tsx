import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { tasksApi } from '@/shared/api/client';
import { endOfDueDay } from './dueDate';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated?: (taskId: string) => void;
}

const UNASSIGNED = 'unassigned';

export function CreateTaskDialog({ open, onOpenChange, onCreated }: Props) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [priority, setPriority] = useState('medium');
  const [taskType, setTaskType] = useState('task');
  const [assigneeId, setAssigneeId] = useState(UNASSIGNED);
  const [dueDate, setDueDate] = useState('');

  // Only the people the org chart will actually accept: offering everybody
  // meant filling in the whole form and being refused on submit.
  const { data: usersData } = useQuery({
    queryKey: ['assignable-users'],
    queryFn: () => tasksApi.assignableUsers(),
    enabled: open,
  });
  const users = usersData?.data ?? [];

  const reset = () => {
    setTitle('');
    setDescription('');
    setPriority('medium');
    setTaskType('task');
    setAssigneeId(UNASSIGNED);
    setDueDate('');
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
      title="New task"
      description="Only a title is required."
      submitLabel="Create task"
      disabledReason={!title.trim() ? 'Give the task a title' : undefined}
      onSubmit={async () => {
        const res = await tasksApi.create({
          title: title.trim(),
          description: description.trim() || undefined,
          priority,
          taskType,
          assigneeId: assigneeId === UNASSIGNED ? undefined : assigneeId,
          // The API takes RFC3339; a date input gives us a plain day.
          dueDate: dueDate ? endOfDueDay(dueDate) : undefined,
        });
        await queryClient.invalidateQueries({ queryKey: ['tasks'] });
        queryClient.invalidateQueries({ queryKey: ['dashboard'] });
        onCreated?.(res.data.id);
        reset();
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="taskTitle">Title</Label>
        <Input id="taskTitle" value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
      </div>

      <div className="space-y-2">
        <Label htmlFor="taskDescription">Description (optional)</Label>
        <Textarea id="taskDescription" value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="taskPriority">Priority</Label>
          <Select value={priority} onValueChange={setPriority}>
            <SelectTrigger id="taskPriority"><SelectValue /></SelectTrigger>
            <SelectContent>
              {['low', 'medium', 'high', 'urgent'].map((p) => (
                <SelectItem key={p} value={p}>{p}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label htmlFor="taskType">Type</Label>
          <Select value={taskType} onValueChange={setTaskType}>
            <SelectTrigger id="taskType"><SelectValue /></SelectTrigger>
            <SelectContent>
              {['task', 'bug', 'story', 'epic', 'subtask'].map((t) => (
                <SelectItem key={t} value={t}>{t}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label htmlFor="taskAssignee">Assignee</Label>
          <Select value={assigneeId} onValueChange={setAssigneeId}>
            <SelectTrigger id="taskAssignee"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={UNASSIGNED}>Unassigned</SelectItem>
              {users.map((u: any) => (
                <SelectItem key={u.userId} value={u.userId}>{u.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label htmlFor="taskDueDate">Due date</Label>
          <Input id="taskDueDate" type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
        </div>
      </div>
    </FormDialog>
  );
}
