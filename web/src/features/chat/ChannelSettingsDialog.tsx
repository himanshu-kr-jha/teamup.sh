import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { UserPlus } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { Button } from '@/shared/components/ui/button';
import { Badge } from '@/shared/components/ui/badge';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/components/ui/tabs';
import { FormDialog } from '@/shared/components/FormDialog';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { PeoplePicker, addEach, addLabel } from '@/shared/components/PeoplePicker';
import { channelsApi, filesApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { AttachmentChip } from '@/features/files/AttachmentChip';
import { formatRelativeTime } from '@/shared/utils/cn';
import { useAuth } from '@/features/auth/AuthProvider';
import { PERMS, can } from '@/shared/permissions';

/**
 * The settings button in the channel header did nothing at all, so there was no
 * way to see who was in a channel, add anybody to a private one, or find a file
 * somebody had shared earlier. There was not even a route to list members.
 */
export function ChannelSettingsDialog({
  channel,
  onOpenChange,
}: {
  channel: { id: string; name?: string; type?: string } | null;
  onOpenChange: (open: boolean) => void;
}) {
  const channelId = channel?.id;
  const toast = useToast();
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [picked, setPicked] = useState<string[]>([]);

  const { data: membersData, isLoading: membersLoading } = useQuery({
    queryKey: ['channel-members', channelId],
    queryFn: () => channelsApi.listMembers(channelId!),
    enabled: !!channelId,
  });
  const { data: mediaData, isLoading: mediaLoading } = useQuery({
    queryKey: ['attachments', channelId],
    queryFn: () => filesApi.list('chat', channelId!),
    enabled: !!channelId,
  });
  const { data: usersData } = useQuery({
    queryKey: ['users'],
    queryFn: () => orgApi.listUsers(),
    enabled: !!channelId,
  });

  const members = membersData?.data ?? [];
  const { user } = useAuth();
  // What the server lets this person do: a channel's owners and admins manage
  // its members, and so does whoever administers every channel. A direct
  // message is the two people in it, and neither manages the other.
  const mine = members.find((m: any) => m.userId === user?.id);
  const managesMembers =
    can(user, PERMS.channelsAll) || (channel?.type !== 'dm' && (mine?.role === 'owner' || mine?.role === 'admin'));
  const media = mediaData?.data ?? [];
  const orgUsers = usersData?.data ?? [];
  const candidates = orgUsers.filter((u: any) => !members.some((m: any) => m.userId === u.id));

  const refreshMembers = () =>
    queryClient.invalidateQueries({ queryKey: ['channel-members', channelId] });

  const remove = async (userId: string) => {
    try {
      await channelsApi.removeMember(channelId!, userId);
      refreshMembers();
      queryClient.invalidateQueries({ queryKey: ['channels'] });
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not remove them from this channel.'));
    }
  };

  return (
    <>
      <Dialog open={!!channel} onOpenChange={onOpenChange}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{channel?.name ?? 'Channel'}</DialogTitle>
            <DialogDescription>
              {channel?.type === 'announcement'
                ? 'Everyone in the organization sees this channel. Only administrators can post.'
                : channel?.type === 'public'
                  ? 'Everyone in the organization can read and post here.'
                  : 'Only the people listed here can read and post.'}
            </DialogDescription>
          </DialogHeader>

          <Tabs defaultValue="members">
            <TabsList className="w-full">
              <TabsTrigger value="members" className="flex-1">
                Members{members.length ? ` (${members.length})` : ''}
              </TabsTrigger>
              <TabsTrigger value="media" className="flex-1">Recent files</TabsTrigger>
            </TabsList>

            <TabsContent value="members" className="space-y-3">
              {managesMembers && (
                <Button size="sm" variant="outline" className="w-full" onClick={() => setAdding(true)}>
                  <UserPlus className="mr-2 h-4 w-4" />
                  Add members
                </Button>
              )}
              {membersLoading && <p className="text-sm text-muted-foreground">Loading members…</p>}
              {!membersLoading && members.length === 0 && (
                <p className="text-sm text-muted-foreground">Nobody is in this channel yet.</p>
              )}
              <ul className="max-h-72 space-y-1 overflow-y-auto">
                {members.map((m: any) => (
                  <li key={m.userId} className="flex items-center gap-3 rounded-md p-2 hover:bg-accent">
                    <PersonAvatar userId={m.userId} name={m.name} src={m.avatarUrl} fallbackClassName="bg-primary/10 font-medium text-primary" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{m.name}</p>
                      <p className="truncate text-xs text-muted-foreground">{m.email}</p>
                    </div>
                    {m.role !== 'member' && <Badge variant="secondary">{m.role}</Badge>}
                    {/* Anybody may leave a channel; only those who manage it remove others. */}
                    {(m.userId === user?.id ? channel?.type !== 'dm' : managesMembers) && (
                      <button
                        onClick={() => remove(m.userId)}
                        aria-label={`Remove ${m.name}`}
                        className="shrink-0 text-muted-foreground hover:text-foreground"
                      >
                        ×
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </TabsContent>

            <TabsContent value="media" className="space-y-2">
              {mediaLoading && <p className="text-sm text-muted-foreground">Loading files…</p>}
              {!mediaLoading && media.length === 0 && (
                <p className="text-sm text-muted-foreground">Nothing has been shared here yet.</p>
              )}
              <ul className="max-h-72 space-y-2 overflow-y-auto">
                {media.map((a: any) => (
                  <li key={a.id} className="flex items-center justify-between gap-2">
                    <AttachmentChip attachment={a} />
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {a.createdAt ? formatRelativeTime(a.createdAt) : null}
                    </span>
                  </li>
                ))}
              </ul>
            </TabsContent>
          </Tabs>
        </DialogContent>
      </Dialog>

      <FormDialog
        open={adding}
        onOpenChange={(o) => {
          if (!o) setPicked([]);
          setAdding(o);
        }}
        title="Add members"
        description="Tick everyone to add. They will each be notified that you added them."
        submitLabel={addLabel(picked.length, 'Add member', (n) => `Add ${n} members`)}
        disabledReason={picked.length === 0 ? 'Choose somebody to add.' : undefined}
        onSubmit={async () => {
          try {
            await addEach(
              picked,
              (id) => channelsApi.addMember(channelId!, id),
              (id) => candidates.find((u: any) => u.id === id)?.name ?? 'somebody',
              setPicked
            );
            setPicked([]);
          } finally {
            refreshMembers();
            queryClient.invalidateQueries({ queryKey: ['channels'] });
          }
        }}
      >
        <PeoplePicker
          people={candidates.map((u: any) => ({ id: u.id, name: u.name, email: u.email, avatarUrl: u.avatarUrl }))}
          selected={picked}
          onChange={setPicked}
          empty="Everybody in the organization is already in this channel."
        />
      </FormDialog>
    </>
  );
}
