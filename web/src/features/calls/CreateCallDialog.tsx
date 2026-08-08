import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { callsApi, orgApi } from '@/shared/api/client';
import { useAuth } from '@/features/auth/AuthProvider';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (callId: string) => void;
}

export function CreateCallDialog({ open, onOpenChange, onCreated }: Props) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState('');
  const [callType, setCallType] = useState<'video' | 'audio'>('video');
  const [invitees, setInvitees] = useState<string[]>([]);

  const { data } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: open });
  const members = (data?.data ?? []).filter((u: any) => u.id !== user?.id);

  const reset = () => {
    setTitle('');
    setCallType('video');
    setInvitees([]);
  };

  const toggle = (id: string) =>
    setInvitees((current) => (current.includes(id) ? current.filter((x) => x !== id) : [...current, id]));

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
      title="Start a call"
      description="Everyone you invite is notified and can join from their call list."
      submitLabel="Start call"
      onSubmit={async () => {
        const res = await callsApi.create({
          title: title.trim() || undefined,
          callType,
          invitees,
        });
        await queryClient.invalidateQueries({ queryKey: ['calls'] });
        onCreated(res.data.id);
        reset();
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="callTitle">Title (optional)</Label>
        <Input
          id="callTitle"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Sprint planning"
          autoFocus
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="callType">Type</Label>
        <Select value={callType} onValueChange={(v) => setCallType(v as 'video' | 'audio')}>
          <SelectTrigger id="callType"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="video">Video</SelectItem>
            <SelectItem value="audio">Audio only</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label>Invite</Label>
        {members.length === 0 && (
          <p className="text-sm text-muted-foreground">Nobody else is in this organization yet.</p>
        )}
        <div className="max-h-40 space-y-1 overflow-y-auto">
          {members.map((m: any) => (
            <label key={m.id} className="flex cursor-pointer items-center gap-2 rounded p-1 hover:bg-accent">
              <input
                type="checkbox"
                checked={invitees.includes(m.id)}
                onChange={() => toggle(m.id)}
                className="h-4 w-4"
              />
              <span className="text-sm">{m.name}</span>
            </label>
          ))}
        </div>
      </div>
    </FormDialog>
  );
}
