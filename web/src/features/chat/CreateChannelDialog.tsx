import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { channelsApi, orgApi } from '@/shared/api/client';
import { PeoplePicker, addEach } from '@/shared/components/PeoplePicker';
import { useAuth } from '@/features/auth/AuthProvider';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (channelId: string) => void;
}

export function CreateChannelDialog({ open, onOpenChange, onCreated }: Props) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [type, setType] = useState('group');
  const [description, setDescription] = useState('');
  const [memberIds, setMemberIds] = useState<string[]>([]);
  // Created already, when only adding its people failed: retrying must not make a second one.
  const [createdId, setCreatedId] = useState<string | null>(null);
  const { user } = useAuth();
  const { data: usersData } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: open });
  const others = (usersData?.data ?? []).filter((u: any) => u.id !== user?.id);

  const reset = () => {
    setName('');
    setType('group');
    setDescription('');
    setMemberIds([]);
    setCreatedId(null);
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
      title="New channel"
      description="Channels are where conversations happen. Public channels can be read by anyone in your organization."
      submitLabel={createdId ? 'Try again' : 'Create channel'}
      disabledReason={!name.trim() ? 'Give the channel a name' : undefined}
      onSubmit={async () => {
        let id = createdId;
        if (!id) {
          const res = await channelsApi.create({
            name: name.trim(),
            type,
            description: description.trim() || undefined,
          });
          id = res.data.id as string;
          setCreatedId(id);
        }
        // Everybody is in a public channel already; a private one has only who is added.
        if (type === 'group' && memberIds.length > 0) {
          await addEach(
            memberIds,
            (userId) => channelsApi.addMember(id!, userId),
            (userId) => others.find((u: any) => u.id === userId)?.name ?? 'somebody',
            setMemberIds
          ).catch(async (err) => {
            await queryClient.invalidateQueries({ queryKey: ['channels'] });
            throw new Error(`The channel was made. ${(err as Error).message}`);
          });
        }
        await queryClient.invalidateQueries({ queryKey: ['channels'] });
        onCreated(id);
        reset();
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="channelName">Name</Label>
        <Input
          id="channelName"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="deployments"
          autoFocus
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="channelType">Visibility</Label>
        <Select value={type} onValueChange={setType}>
          <SelectTrigger id="channelType"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="group">Private — only invited members</SelectItem>
            <SelectItem value="public">Public — anyone in the organization</SelectItem>
            <SelectItem value="announcement">Announcements</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label htmlFor="channelDescription">Description (optional)</Label>
        <Textarea
          id="channelDescription"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={2}
          placeholder="What is this channel for?"
        />
      </div>

      {type === 'group' && (
        <div className="space-y-2">
          <Label>Members (optional)</Label>
          <PeoplePicker
            people={others.map((u: any) => ({ id: u.id, name: u.name, email: u.email, avatarUrl: u.avatarUrl }))}
            selected={memberIds}
            onChange={setMemberIds}
            empty="Nobody else is in the organization yet."
            listClassName="max-h-44"
          />
        </div>
      )}
    </FormDialog>
  );
}
