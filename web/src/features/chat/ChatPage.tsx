import { useState, useEffect, useRef, useMemo } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import {
  Hash, Send, Paperclip, Smile, Bell, Settings, ChevronLeft, MessageSquare, Video, User,
  Megaphone, MoreVertical, Pencil, Trash2, Eye, Ban, Check, CheckCheck, PhoneMissed,
} from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Avatar, AvatarFallback } from '@/shared/components/ui/avatar';
import { cn, formatRelativeTime } from '@/shared/utils/cn';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { callsApi, channelsApi, filesApi } from '@/shared/api/client';
import { useUpload, type UploadedFile } from '@/features/files/useUpload';
import { useToast } from '@/shared/hooks/useToast';
import { apiErrorMessage } from '@/shared/api/errors';
import { AttachmentChip } from '@/features/files/AttachmentChip';
import { ChannelSettingsDialog } from './ChannelSettingsDialog';
import { NewDirectMessageDialog } from './NewDirectMessageDialog';
import { useAuth } from '@/features/auth/AuthProvider';
import { useUIStore } from '@/shared/store';
import { CreateChannelDialog } from './CreateChannelDialog';
import { usePresence, type PresenceStatus } from '@/features/presence/usePresence';
import { PresenceDot, presenceLabel } from '@/features/presence/PresenceDot';

interface Message {
  id: string;
  channelId: string;
  userId: string;
  userName: string;
  userAvatar?: string;
  content: string;
  attachments: any[];
  replyTo?: string;
  reactions: Record<string, string[]>;
  createdAt: string;
  editedAt?: string;
  deletedAt?: string;
  /** How many other people in the channel have read it. */
  readCount?: number;
  /**
   * "user" for something somebody wrote, "event" for something that happened
   * — a missed call, say. Absent on older messages, which are all "user".
   */
  kind?: string;
  pending?: boolean;
}

export function ChatPage() {
  const { user } = useAuth();
  const navigate = useNavigate();
  const { on } = useWebSocket();
  const queryClient = useQueryClient();
  const { activeChannelId, setActiveChannelId } = useUIStore();
  const params = useParams<{ channelId?: string }>();
  const [message, setMessage] = useState('');
  // There is not room for the list and the conversation at once on a phone,
  // so it shows one: the list until you pick something, the conversation after.
  const [showSidebar, setShowSidebar] = useState(() => {
    if (typeof window === 'undefined' || window.innerWidth >= 1024) return true;
    // Arriving at a conversation by its address should show the conversation,
    // not the list you did not ask for.
    return !params.channelId;
  });
  const [createChannelOpen, setCreateChannelOpen] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  // Uploaded but not yet sent. A file used to appear in the channel the instant
  // it finished uploading; now it waits here until you press Send, so it can go
  // on its own or alongside a message.
  const [staged, setStaged] = useState<UploadedFile[]>([]);
  const [newChatOpen, setNewChatOpen] = useState(false);
  const [startingCall, setStartingCall] = useState(false);
  // What we last told the server was read, per channel, so scrolling or a
  // re-render does not repeat the call.
  const lastReadRef = useRef<Record<string, string>>({});
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const channelId = params.channelId || activeChannelId;

  // Fetch channels
  const { data: channelsResponse, isLoading: channelsLoading } = useQuery({
    queryKey: ['channels'],
    queryFn: () => channelsApi.list(),
  });

  const channels = channelsResponse?.data || [];

  // Fetch messages for active channel
  const { data: messagesResponse, isLoading: messagesLoading } = useQuery({
    queryKey: ['messages', channelId],
    queryFn: () => channelsApi.getMessages(channelId!),
    enabled: !!channelId,
    staleTime: 0,
    refetchOnMount: 'always',
  });

  // Memoised so the fallback [] doesn't produce a new array every render and
  // retrigger the scroll-to-bottom effect below.
  const messages = useMemo(() => messagesResponse?.data || [], [messagesResponse]);

  const activeChannel = channels.find((c) => c.id === channelId);
  const { statusOf, personOf } = usePresence();
  const toast = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { upload, progress, error: uploadError } = useUpload('chat', channelId || '');

  useEffect(() => {
    if (uploadError) toast.error(uploadError);
    // toast is rebuilt each render; the error is the real trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uploadError]);

  // The messages query goes through getList, so the cache entry is the whole
  // axios response and the array lives on `.data`. Every writer below used to
  // treat the entry itself as the array, which threw inside onMutate and meant
  // the send request was never issued at all.
  const writeMessages = (
    channel: string,
    update: (messages: Message[]) => Message[]
  ) =>
    queryClient.setQueryData(['messages', channel], (old: any) =>
      old ? { ...old, data: update(old.data ?? []) } : old
    );

  const sendMessageMutation = useMutation({
    mutationFn: (data: { channelId: string; content: string; tempId: string; attachmentIds?: string[] }) =>
      channelsApi.sendMessage(data.channelId, data),
    onMutate: async (variables) => {
      await queryClient.cancelQueries({ queryKey: ['messages', variables.channelId] });
      const previous = queryClient.getQueryData(['messages', variables.channelId]);
      const tempMessage: Message = {
        id: variables.tempId,
        channelId: variables.channelId,
        userId: user?.id || '',
        userName: user?.name || 'You',
        content: variables.content,
        attachments: variables.attachmentIds?.length ? staged : [],
        reactions: {},
        createdAt: new Date().toISOString(),
        pending: true,
      };
      // History is oldest-first, so a new message belongs at the end.
      writeMessages(variables.channelId, (messages) => [...messages, tempMessage]);
      return { previous };
    },
    onError: (err, variables, context) => {
      queryClient.setQueryData(['messages', variables.channelId], context?.previous);
      toast.error(apiErrorMessage(err, 'Could not send that message.'));
    },
    onSuccess: (data, variables) => {
      writeMessages(variables.channelId, (messages) =>
        messages.map((m) => (m.id === variables.tempId ? { ...(data.data as Message), pending: false } : m))
      );
    },
    onSettled: (data, error, variables) => {
      queryClient.invalidateQueries({ queryKey: ['messages', variables.channelId] });
    },
  });

  // Handle incoming messages via WebSocket
  useEffect(() => {
    const unsubscribe = on('chat.message', (msg) => {
      // The frame carries {message: {...}}; reading the payload directly gave
      // an object with no channelId, so every live message was discarded and
      // the only way to see one was to reload.
      const incoming = ((msg.payload as any)?.message ?? msg.payload) as Message;
      if (!incoming?.id) return;
      // The sound and the desktop notification are raised by
      // ChatNotifications, which listens wherever you are in the app. This
      // only keeps the conversation on screen up to date.

      writeMessages(incoming.channelId, (messages) =>
        messages.some((m) => m.id === incoming.id) ? messages : [...messages, incoming]
      );

      if (incoming.channelId === channelId) {
        // You are reading it, so it is already read.
        markChannelRead(incoming.channelId, incoming.id);
      } else {
        // Otherwise it is unread, and that count lives on the channel list.
        queryClient.invalidateQueries({ queryKey: ['channels'] });
      }
    });
    return unsubscribe;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, on, queryClient]);

  // Handle sent confirmation
  useEffect(() => {
    const unsubscribe = on('chat.sent', (msg) => {
      const { tempId, messageId, timestamp } = msg.payload;
      if (!channelId) return;
      writeMessages(channelId, (messages) =>
        messages.map((m) => (m.id === tempId ? { ...m, id: messageId, createdAt: timestamp, pending: false } : m))
      );
    });
    return unsubscribe;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, on, queryClient]);

  // Scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  // Reading a conversation marks it read. Nothing used to call this, so
  // last_read_at was never set and every message ever sent stayed unread.
  useEffect(() => {
    if (!channelId || messages.length === 0) return;
    const latest = messages[messages.length - 1];
    if (!latest?.id || latest.pending) return;
    markChannelRead(channelId, latest.id);
    // Keyed on the last message so re-reading the same history is one call.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, messages[messages.length - 1]?.id]);

  // Best effort: failing to record a read marker must not interrupt reading.
  const markChannelRead = (id: string, messageId: string) => {
    if (lastReadRef.current[id] === messageId) return;
    lastReadRef.current[id] = messageId;
    channelsApi
      .markRead(id, messageId)
      .then(() => queryClient.invalidateQueries({ queryKey: ['channels'] }))
      .catch(() => {
        // It will be marked again on the next message.
        delete lastReadRef.current[id];
      });
  };

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    if (!channelId) return;
    // A file on its own is a message, so an empty box is fine if something is
    // attached.
    if (!message.trim() && staged.length === 0) return;
    const tempId = `temp-${Date.now()}`;
    sendMessageMutation.mutate({
      channelId,
      content: message,
      tempId,
      attachmentIds: staged.map((f) => f.id),
    });
    setMessage('');
    setStaged([]);
  };

  // Calls the whole channel: everybody in it is rung. A public channel has the
  // whole organization in it, a private one only its members, so the channel
  // decides who can be reached without a separate rule here.
  const startCall = async () => {
    if (!channelId || startingCall) return;
    setStartingCall(true);
    try {
      const { data: call } = await callsApi.create({
        channelId,
        callType: 'video',
        title: activeChannel?.name ?? undefined,
      });
      navigate(`/calls/${call.id}`);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not start that call.'));
    } finally {
      setStartingCall(false);
    }
  };

  const channelSubtitle = !channelId
    ? 'Pick a conversation to start reading'
    : activeChannel?.type === 'dm'
      ? presenceLabel(
          statusOf(activeChannel?.counterpartId),
          personOf(activeChannel?.counterpartId)?.note
        )
      : activeChannel?.type === 'announcement'
        ? 'Announcements — only administrators can post'
        : `${activeChannel?.memberCount ?? 0} ${activeChannel?.memberCount === 1 ? 'member' : 'members'}`;

  const rooms = channels.filter((c: any) => c.type !== 'dm');
  const directMessages = channels.filter((c: any) => c.type === 'dm');

  const handleChannelClick = (id: string) => {
    setActiveChannelId(id);
    navigate(`/chat/${id}`);
    // On a phone, picking a conversation is how you get to it.
    if (window.innerWidth < 1024) setShowSidebar(false);
  };

  if (channelsLoading) {
    return <div className="flex h-full items-center justify-center">Loading channels...</div>;
  }

  return (
    <div className="flex h-full flex-col bg-background">

      <div className="flex h-full min-w-0">
        {/* Sidebar */}
        <aside className={cn(
          'z-30 w-full shrink-0 border-r border-border bg-card lg:relative lg:w-72',
          // On a phone this is the whole screen, and only one of the two is
          // shown at a time.
          showSidebar ? 'block' : 'hidden lg:block'
        )}>
          <div className="flex h-full flex-col">
            {/* Header */}
            <div className="flex h-16 items-center justify-between border-b border-border px-4">
              <Link to="/chat" className="text-lg font-semibold">
                Conversations
              </Link>
              <span className="text-xs text-muted-foreground">
                {channels.length} {channels.length === 1 ? 'open' : 'open'}
              </span>
            </div>

            {/* Channels and conversations, kept apart: a room and a person
                are not the same kind of thing. */}
            <div className="flex-1 overflow-y-auto p-2">
              {channels.length === 0 && (
                <p className="px-3 py-8 text-center text-sm text-muted-foreground">
                  Nothing here yet. Start a chat or create a channel.
                </p>
              )}

              {[
                { label: 'Channels', items: rooms },
                { label: 'Direct messages', items: directMessages },
              ].map((group) =>
                group.items.length === 0 ? null : (
                  <div key={group.label} className="mb-4">
                    <p className="px-3 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      {group.label}
                    </p>
                    <div className="space-y-0.5">
                      {group.items.map((channel) => {
                        const active = channel.id === channelId;
                        return (
                          <button
                            key={channel.id}
                            onClick={() => handleChannelClick(channel.id)}
                            className={cn(
                              'flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm transition-colors',
                              active
                                ? 'bg-primary text-primary-foreground font-medium'
                                : 'text-foreground/80 hover:bg-accent hover:text-accent-foreground'
                            )}
                          >
                            {channel.type === 'dm' ? (
                              // A conversation with a person shows whether that
                              // person is around, which is the thing you want to
                              // know before starting to type.
                              <span className="relative flex h-4 w-4 shrink-0 items-center justify-center">
                                <User className="h-4 w-4 opacity-70" />
                                <PresenceDot
                                  status={statusOf(channel.counterpartId)}
                                  note={personOf(channel.counterpartId)?.note}
                                  className="absolute -bottom-1 -right-1 h-2 w-2"
                                />
                              </span>
                            ) : channel.type === 'announcement' ? (
                              <Megaphone className="h-4 w-4 shrink-0 opacity-70" />
                            ) : (
                              <Hash className="h-4 w-4 shrink-0 opacity-70" />
                            )}
                            <span className={cn('flex-1 truncate', channel.unreadCount > 0 && !active && 'font-semibold')}>
                              {channel.name || 'Unnamed'}
                            </span>
                            {channel.unreadCount > 0 && (
                              <span
                                className={cn(
                                  'flex h-5 min-w-[1.25rem] items-center justify-center rounded-full px-1.5 text-xs font-semibold',
                                  active ? 'bg-primary-foreground/20 text-primary-foreground' : 'bg-primary text-primary-foreground'
                                )}
                              >
                                {channel.unreadCount > 9 ? '9+' : channel.unreadCount}
                              </span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )
              )}
            </div>

            {/* New channel button */}
            <div className="space-y-2 p-2 border-t border-border">
              <Button variant="outline" className="w-full" onClick={() => setNewChatOpen(true)}>
                <User className="mr-2 h-4 w-4" />
                New chat
              </Button>
              <Button variant="outline" className="w-full" onClick={() => setCreateChannelOpen(true)}>
                <MessageSquare className="mr-2 h-4 w-4" />
                New Channel
              </Button>
            </div>
          </div>
        </aside>

        {/* Main chat area */}
        <div className={cn(
          'flex min-w-0 flex-1 flex-col',
          showSidebar ? 'hidden lg:flex' : 'flex'
        )}>
          {/* Channel header */}
          <div className="flex h-16 items-center gap-4 px-4 border-b border-border">
            <div className="flex min-w-0 items-center gap-2">
              <Button
                variant="ghost"
                size="icon"
                className="shrink-0 lg:hidden"
                aria-label="Back to conversations"
                onClick={() => setShowSidebar(true)}
              >
                <ChevronLeft className="h-5 w-5" />
              </Button>
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/10">
                {activeChannel?.type === 'dm' ? (
                  <User className="h-5 w-5 text-primary" />
                ) : activeChannel?.type === 'announcement' ? (
                  <Megaphone className="h-5 w-5 text-primary" />
                ) : (
                  <Hash className="h-5 w-5 text-primary" />
                )}
              </div>
              <div className="min-w-0">
                <h2 className="flex items-center gap-2 truncate font-semibold leading-tight">
                  {activeChannel?.name || (channelId ? 'Channel' : 'No channel selected')}
                  {activeChannel?.type === 'dm' && (
                    <PresenceDot
                      status={statusOf(activeChannel?.counterpartId)}
                      note={personOf(activeChannel?.counterpartId)?.note}
                    />
                  )}
                </h2>
                <p className="truncate text-sm text-muted-foreground">{channelSubtitle}</p>
              </div>
            </div>
            <div className="flex-1" />
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="icon"
                aria-label="Start a call"
                disabled={!activeChannel || startingCall}
                onClick={startCall}
              >
                <Video className="h-5 w-5" />
              </Button>
              <Button variant="ghost" size="icon" aria-label="Notifications"><Bell className="h-5 w-5" /></Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Channel settings"
                disabled={!activeChannel}
                onClick={() => setShowSettings(true)}
              >
                <Settings className="h-5 w-5" />
              </Button>
            </div>
          </div>

          {/* Messages */}
          <div className="flex-1 space-y-1 overflow-y-auto p-4">
            {messagesLoading ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                Loading messages…
              </div>
            ) : messages.length === 0 ? (
              <div className="flex h-full flex-col items-center justify-center text-muted-foreground">
                <MessageSquare className="mb-4 h-12 w-12 opacity-40" aria-hidden="true" />
                <p className="text-lg">No messages yet</p>
                <p className="text-sm">
                  {activeChannel?.type === 'dm'
                    ? `Say hello to ${activeChannel?.name ?? 'them'}.`
                    : 'Be the first to say something.'}
                </p>
              </div>
            ) : (
              <>
                {messages.map((msg, i) => {
                  const previous = messages[i - 1];
                  // A run of messages from one person in quick succession
                  // reads as one turn in the conversation, so only the first
                  // carries their name and picture.
                  const sameAuthor = previous?.userId === msg.userId;
                  const closeInTime =
                    previous != null &&
                    new Date(msg.createdAt).getTime() - new Date(previous.createdAt).getTime() < 5 * 60 * 1000;
                  const newDay = !previous || !sameDay(previous.createdAt, msg.createdAt);

                  if (msg.kind === 'event') {
                    return (
                      <div key={msg.id}>
                        {newDay && <DaySeparator when={msg.createdAt} />}
                        <CallEvent message={msg} isOwn={msg.userId === user?.id} />
                      </div>
                    );
                  }

                  return (
                    <div key={msg.id}>
                      {newDay && <DaySeparator when={msg.createdAt} />}
                      <MessageBubble
                        message={msg}
                        isOwn={msg.userId === user?.id}
                        status={statusOf(msg.userId)}
                        note={personOf(msg.userId)?.note}
                        grouped={sameAuthor && closeInTime && !newDay}
                        channelId={channelId!}
                      />
                    </div>
                  );
                })}
                <div ref={messagesEndRef} />
              </>
            )}
          </div>

          {/* Message input */}
          {staged.length > 0 && (
            <div className="border-t border-border bg-muted/30 px-4 py-2">
              <p className="mb-1.5 text-xs font-medium text-muted-foreground">
                {staged.length === 1 ? 'Attached' : `${staged.length} attached`} — press Send
              </p>
              <div className="flex flex-wrap gap-2">
                {staged.map((file) => (
                  <span key={file.id} className="flex items-center gap-1">
                    <AttachmentChip attachment={{ ...file, sizeBytes: file.size }} />
                    <button
                      type="button"
                      onClick={() => {
                        setStaged((all) => all.filter((f) => f.id !== file.id));
                        // Throw the bytes away too, rather than leaving them
                        // in storage for the sweep to find a day later.
                        filesApi.discard(file.id).catch(() => {
                          // Best effort: the sweep collects it either way.
                        });
                      }}
                      aria-label={`Remove ${file.filename}`}
                      className="text-muted-foreground hover:text-foreground"
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            </div>
          )}

          <form onSubmit={handleSend} className="p-4 border-t border-border">
            <div className="flex items-end gap-2">
              <input
                ref={fileInputRef}
                type="file"
                className="hidden"
                aria-label="Attach a file"
                onChange={async (e) => {
                  const file = e.target.files?.[0];
                  e.target.value = '';
                  if (!file || !channelId) return;
                  const uploaded = await upload(file);
                  if (uploaded) setStaged((all) => [...all, uploaded]);
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="shrink-0"
                disabled={!channelId || progress !== null}
                onClick={() => fileInputRef.current?.click()}
                aria-label="Attach a file"
              >
                {progress !== null ? (
                  <span className="text-xs font-medium">{progress}%</span>
                ) : (
                  <Paperclip className="h-5 w-5" />
                )}
              </Button>
              <div className="flex-1 relative">
                <Input
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder={
                    activeChannel?.type === 'dm'
                      ? `Message ${activeChannel?.name ?? ''}`.trim()
                      : activeChannel?.name
                        ? `Message #${activeChannel.name}`
                        : 'Type a message…'
                  }
                  aria-label="Message"
                  disabled={!channelId}
                  className="pr-10"
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault();
                      handleSend(e);
                    }
                  }}
                />
                <div className="absolute bottom-1 right-1 flex items-center gap-1">
                  <Button variant="ghost" size="icon" type="button" aria-label="Emoji" className="h-8 w-8">
                    <Smile className="h-4 w-4" />
                  </Button>
                </div>
              </div>
              <Button
                type="submit"
                size="icon"
                disabled={!message.trim() && staged.length === 0}
                className="shrink-0"
              >
                <Send className="h-5 w-5" />
              </Button>
            </div>
          </form>
        </div>
      </div>

      <CreateChannelDialog
        open={createChannelOpen}
        onOpenChange={setCreateChannelOpen}
        onCreated={(id) => {
          setActiveChannelId(id);
          navigate(`/chat/${id}`);
        }}
      />

      <NewDirectMessageDialog
        open={newChatOpen}
        onOpenChange={setNewChatOpen}
        onOpened={(id) => {
          queryClient.invalidateQueries({ queryKey: ['channels'] });
          setActiveChannelId(id);
          navigate(`/chat/${id}`);
        }}
      />

      <ChannelSettingsDialog
        channel={showSettings ? activeChannel ?? null : null}
        onOpenChange={(open) => !open && setShowSettings(false)}
      />
    </div>
  );
}

function sameDay(a: string, b: string) {
  const x = new Date(a);
  const y = new Date(b);
  return x.toDateString() === y.toDateString();
}

/** "Today", "Yesterday", or the date — so a long history is navigable. */
function DaySeparator({ when }: { when: string }) {
  const day = new Date(when);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);

  const label =
    day.toDateString() === today.toDateString()
      ? 'Today'
      : day.toDateString() === yesterday.toDateString()
        ? 'Yesterday'
        : day.toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' });

  return (
    <div className="my-4 flex items-center gap-3">
      <div className="h-px flex-1 bg-border" />
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <div className="h-px flex-1 bg-border" />
    </div>
  );
}

/**
 * Something that happened rather than something anybody said.
 *
 * Centred and unattributed, because a missed call is not a message from the
 * person who called — it is the conversation telling you what you missed, and
 * dressing it as their message would have you replying to nobody.
 */
function CallEvent({ message, isOwn }: { message: Message; isOwn: boolean }) {
  return (
    <div className="flex justify-center py-1.5">
      <span className="flex items-center gap-2 rounded-full bg-muted px-3 py-1 text-xs text-muted-foreground">
        <PhoneMissed className="h-3.5 w-3.5" aria-hidden="true" />
        {/* The caller knows they called; what they did not know is that it went
            unanswered. The other person needs to be told it happened at all. */}
        <span>{isOwn ? 'No answer' : `${message.content} from ${message.userName}`}</span>
        <span>
          {new Date(message.createdAt).toLocaleTimeString(undefined, {
            hour: '2-digit',
            minute: '2-digit',
          })}
        </span>
      </span>
    </div>
  );
}

function MessageBubble({
  message,
  isOwn,
  status,
  note,
  grouped,
  channelId,
}: {
  message: Message;
  isOwn: boolean;
  status: PresenceStatus;
  note?: string;
  /** Part of a run from the same person: no repeated name or picture. */
  grouped: boolean;
  channelId: string;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(message.content);
  const [showingReads, setShowingReads] = useState(false);

  const deleted = !!message.deletedAt;
  const time = new Date(message.createdAt).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  });
  // Editing closes five minutes after sending, so a conversation people have
  // already read does not change underneath them.
  const withinEditWindow =
    Date.now() - new Date(message.createdAt).getTime() < 5 * 60 * 1000;

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['messages', channelId] });

  const saveEdit = async () => {
    const content = draft.trim();
    if (!content || content === message.content) {
      setEditing(false);
      return;
    }
    try {
      await channelsApi.editMessage(message.id, content);
      setEditing(false);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not edit that message.'));
    }
  };

  const remove = async () => {
    try {
      await channelsApi.deleteMessage(message.id);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not delete that message.'));
    }
  };

  return (
    <>
      <div
        className={cn(
          'group flex w-full items-end gap-2',
          isOwn ? 'justify-end' : 'justify-start',
          grouped ? 'mt-0.5' : 'mt-3'
        )}
      >
        {!isOwn &&
          (grouped ? (
            <div className="w-8 shrink-0" aria-hidden="true" />
          ) : (
            <div className="relative shrink-0">
              <Avatar className="h-8 w-8">
                <AvatarFallback className="text-xs">
                  {(message.userName || '?').charAt(0).toUpperCase()}
                </AvatarFallback>
              </Avatar>
              <PresenceDot status={status} note={note} className="absolute -bottom-0.5 -right-0.5" />
            </div>
          ))}

        <div className={cn('relative flex min-w-0 max-w-[75%] flex-col', isOwn ? 'items-end' : 'items-start')}>
          {!isOwn && !grouped && (
            <p className="mb-1 px-1 text-xs font-medium text-muted-foreground">{message.userName}</p>
          )}

          <div className="flex min-w-0 max-w-full items-end gap-1">
            {/* The menu sits on the side the message came from, so it never
                covers the text. */}
            {isOwn && !deleted && (
              <MessageMenu
                canEdit={withinEditWindow}
                canDelete
                onEdit={() => {
                  setDraft(message.content);
                  setEditing(true);
                }}
                onDelete={remove}
                onShowReads={() => setShowingReads(true)}
              />
            )}

            <div
              className={cn(
                'w-fit max-w-full break-anywhere rounded-2xl px-3.5 py-2 text-sm shadow-sm',
                deleted
                  ? 'border border-dashed border-border bg-transparent italic text-muted-foreground'
                  : isOwn
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-foreground',
                !grouped && !deleted && (isOwn ? 'rounded-tr-sm' : 'rounded-tl-sm'),
                message.pending && 'opacity-60'
              )}
            >
              {deleted ? (
                <span className="flex items-center gap-1.5">
                  <Ban className="h-3.5 w-3.5" aria-hidden="true" />
                  This message was deleted
                </span>
              ) : editing ? (
                <div className="space-y-2">
                  <Input
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    aria-label="Edit message"
                    className="h-8 bg-background text-foreground"
                    autoFocus
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault();
                        void saveEdit();
                      }
                      if (e.key === 'Escape') setEditing(false);
                    }}
                  />
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" className="h-6 text-xs" onClick={() => setEditing(false)}>
                      Cancel
                    </Button>
                    <Button size="sm" className="h-6 text-xs" onClick={saveEdit}>
                      Save
                    </Button>
                  </div>
                </div>
              ) : (
                <>
                  {message.content && (
                    <p className="whitespace-pre-wrap break-anywhere">{message.content}</p>
                  )}
                  {message.attachments?.length > 0 && (
                    <div className={cn('flex flex-wrap gap-2', message.content && 'mt-2')}>
                      {message.attachments.map((a: any) => (
                        <AttachmentChip key={a.id} attachment={a} />
                      ))}
                    </div>
                  )}
                </>
              )}
            </div>

            {!isOwn && !deleted && (
              <MessageMenu
                canEdit={false}
                canDelete={false}
                onShowReads={() => setShowingReads(true)}
              />
            )}
          </div>

          {/* Out of the flow: shown on hover without moving anything. */}
          <span
            className={cn(
              'pointer-events-none absolute bottom-0 flex items-center gap-1 whitespace-nowrap text-[0.7rem] text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100',
              isOwn ? 'right-full mr-2' : 'left-full ml-2',
              (message.pending || (isOwn && !deleted)) && 'opacity-100'
            )}
          >
            {message.pending ? (
              'Sending…'
            ) : (
              <>
                {time}
                {message.editedAt && !deleted && <span>· edited</span>}
                {/* Only the sender is told whether it landed; showing it to
                    everybody would just be noise. */}
                {isOwn && !deleted && (
                  <span title={message.readCount ? `Read by ${message.readCount}` : 'Not read yet'}>
                    {message.readCount ? (
                      <CheckCheck className="h-3.5 w-3.5 text-primary" aria-label="Read" />
                    ) : (
                      <Check className="h-3.5 w-3.5" aria-label="Sent" />
                    )}
                  </span>
                )}
              </>
            )}
          </span>
        </div>
      </div>

      <ReadByDialog
        messageId={showingReads ? message.id : null}
        onClose={() => setShowingReads(false)}
      />
    </>
  );
}

/** The actions available on a single message. */
function MessageMenu({
  canEdit,
  canDelete,
  onEdit,
  onDelete,
  onShowReads,
}: {
  canEdit: boolean;
  canDelete: boolean;
  onEdit?: () => void;
  onDelete?: () => void;
  onShowReads: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Message actions"
          className="h-6 w-6 shrink-0 opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100"
        >
          <MoreVertical className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onShowReads}>
          <Eye className="mr-2 h-4 w-4" />
          Who has read this
        </DropdownMenuItem>
        {onEdit && (
          <DropdownMenuItem
            onSelect={onEdit}
            disabled={!canEdit}
            // Saying why it is unavailable is more use than an item that
            // simply does nothing.
            title={canEdit ? undefined : 'Messages can only be edited for five minutes'}
          >
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </DropdownMenuItem>
        )}
        {canDelete && onDelete && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive" onSelect={onDelete}>
              <Trash2 className="mr-2 h-4 w-4" />
              Delete for everyone
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** Who has read a message, in the order they read it. */
function ReadByDialog({ messageId, onClose }: { messageId: string | null; onClose: () => void }) {
  const { data, isLoading } = useQuery({
    queryKey: ['message-reads', messageId],
    queryFn: () => channelsApi.messageReads(messageId!),
    enabled: !!messageId,
  });
  const reads = data?.data ?? [];

  return (
    <Dialog open={!!messageId} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Read by</DialogTitle>
          <DialogDescription>
            Everybody in this conversation who has read as far as this message.
          </DialogDescription>
        </DialogHeader>
        {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
        {!isLoading && reads.length === 0 && (
          <p className="text-sm text-muted-foreground">Nobody has read it yet.</p>
        )}
        <ul className="max-h-64 space-y-1 overflow-y-auto">
          {reads.map((r: any) => (
            <li key={r.userId} className="flex items-center justify-between gap-3 text-sm">
              <span className="truncate">{r.name}</span>
              <span className="shrink-0 text-xs text-muted-foreground">
                {formatRelativeTime(r.readAt)}
              </span>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
