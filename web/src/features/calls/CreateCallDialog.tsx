import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { PeoplePicker, type Pickable } from '@/shared/components/PeoplePicker';
import { callsApi, orgApi } from '@/shared/api/client';
import { useAuth } from '@/features/auth/AuthProvider';
import { usePresence } from '@/features/presence/usePresence';
import { backOnText, onLeave } from '@/features/presence/leaveNotice';
import { TakeNotesSwitch, useNotesDefault } from './notetaker';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (callId: string) => void;
}

export function CreateCallDialog({ open, onOpenChange, onCreated }: Props) {
  const { user } = useAuth();
  // Whoever is on leave is not rung, so say so beside their name before the call.
  const { personOf } = usePresence();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState('');
  const [callType, setCallType] = useState<'video' | 'audio'>('video');
  const [invitees, setInvitees] = useState<string[]>([]);
  // Notes in the call: the organization's default until changed here.
  const notesDefault = useNotesDefault(open);
  const [takeNotes, setTakeNotes] = useState<boolean | null>(null);

  const { data } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: open });
  const members = useMemo(
    () =>
      ((data?.data ?? []) as Array<Pickable & { isActive?: boolean }>).filter(
        (u) => u.id !== user?.id && u.isActive !== false
      ),
    [data, user?.id]
  );

  const reset = () => {
    setTitle('');
    setCallType('video');
    setInvitees([]);
    setTakeNotes(null);
  };

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
          takeNotes: notesDefault?.available ? (takeNotes ?? notesDefault.everyCall) : undefined,
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
        <Label htmlFor="callInvite">Invite</Label>
        <PeoplePicker
          inputId="callInvite"
          people={members}
          selected={invitees}
          onChange={setInvitees}
          empty="Nobody else is in this organization yet."
          listClassName="max-h-56"
          autoFocus
          note={(m) =>
            onLeave(personOf(m.id)) && (
              <span className="block text-xs text-amber-700 dark:text-amber-400">
                On leave, {backOnText(personOf(m.id)?.until)}; will not be rung
              </span>
            )
          }
        />
      </div>

      {notesDefault?.available && (
        <TakeNotesSwitch id="callNotes" checked={takeNotes ?? notesDefault.everyCall} onCheckedChange={setTakeNotes} />
      )}
    </FormDialog>
  );
}
