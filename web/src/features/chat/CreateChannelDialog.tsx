import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { channelsApi } from '@/shared/api/client';

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

  const reset = () => {
    setName('');
    setType('group');
    setDescription('');
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
      submitLabel="Create channel"
      disabledReason={!name.trim() ? 'Give the channel a name' : undefined}
      onSubmit={async () => {
        const res = await channelsApi.create({
          name: name.trim(),
          type,
          description: description.trim() || undefined,
        });
        await queryClient.invalidateQueries({ queryKey: ['channels'] });
        onCreated(res.data.id);
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
          rows={3}
          placeholder="What is this channel for?"
        />
      </div>
    </FormDialog>
  );
}
