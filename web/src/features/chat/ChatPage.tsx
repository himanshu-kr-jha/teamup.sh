import { useState, useEffect, useRef, useMemo } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { Hash, Send, Paperclip, Smile, Bell, Settings, ChevronLeft, MessageSquare, Video } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Avatar, AvatarFallback } from '@/shared/components/ui/avatar';
import { cn } from '@/shared/utils/cn';
import { formatRelativeTime } from '@/shared/utils/cn';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { channelsApi, filesApi } from '@/shared/api/client';
import { useUpload } from '@/features/files/useUpload';
import { useToast } from '@/shared/hooks/useToast';
import { apiErrorMessage } from '@/shared/api/errors';
import { AttachmentChip } from '@/features/files/AttachmentChip';
import { ChannelSettingsDialog } from './ChannelSettingsDialog';
import { useAuth } from '@/features/auth/AuthProvider';
import { useUIStore } from '@/shared/store';
import { CreateChannelDialog } from './CreateChannelDialog';
import { usePresence, type PresenceStatus } from '@/features/presence/usePresence';
import { PresenceDot } from '@/features/presence/PresenceDot';

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
  const [showSidebar, setShowSidebar] = useState(true);
  const [createChannelOpen, setCreateChannelOpen] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
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
  });

  // Memoised so the fallback [] doesn't produce a new array every render and
  // retrigger the scroll-to-bottom effect below.
  const messages = useMemo(() => messagesResponse?.data || [], [messagesResponse]);

  const activeChannel = channels.find((c) => c.id === channelId);
  const { statusOf } = usePresence();
  const toast = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { upload, progress, error: uploadError } = useUpload('chat', channelId || '');

  // Files already attached to this channel.
  const { data: attachmentsData } = useQuery({
    queryKey: ['attachments', channelId],
    queryFn: () => filesApi.list('chat', channelId!),
    enabled: !!channelId,
  });
  const attachments = attachmentsData?.data ?? [];

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
    mutationFn: (data: { channelId: string; content: string; tempId: string }) => channelsApi.sendMessage(data.channelId, data),
    onMutate: async (variables) => {
      await queryClient.cancelQueries({ queryKey: ['messages', variables.channelId] });
      const previous = queryClient.getQueryData(['messages', variables.channelId]);
      const tempMessage: Message = {
        id: variables.tempId,
        channelId: variables.channelId,
        userId: user?.id || '',
        userName: user?.name || 'You',
        content: variables.content,
        attachments: [],
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
      if (msg.payload.channelId === channelId && channelId) {
        writeMessages(channelId, (messages) =>
          messages.some((m) => m.id === msg.payload.id) ? messages : [...messages, msg.payload]
        );
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

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    if (!message.trim() || !channelId) return;
    const tempId = `temp-${Date.now()}`;
    sendMessageMutation.mutate({ channelId, content: message, tempId });
    setMessage('');
  };

  const handleChannelClick = (id: string) => {
    setActiveChannelId(id);
    navigate(`/chat/${id}`);
  };

  if (channelsLoading) {
    return <div className="flex h-full items-center justify-center">Loading channels...</div>;
  }

  return (
    <div className="flex h-full flex-col bg-background">
      {/* Mobile sidebar toggle */}
      <button
        className="lg:hidden fixed bottom-4 right-4 z-50 p-2 rounded-full bg-primary text-primary-foreground shadow-lg"
        onClick={() => setShowSidebar(!showSidebar)}
        aria-label="Toggle sidebar"
      >
        {showSidebar ? <ChevronLeft className="h-6 w-6" /> : <MessageSquare className="h-6 w-6" />}
      </button>

      <div className={cn('flex h-full', showSidebar ? 'lg:flex-row' : 'lg:flex-col')}>
        {/* Sidebar */}
        <aside className={cn(
          'fixed inset-y-0 left-0 z-40 w-72 bg-card border-r border-border transition-transform duration-200 lg:relative lg:translate-x-0',
          !showSidebar && '-translate-x-full lg:translate-x-0'
        )}>
          <div className="flex h-full flex-col">
            {/* Header */}
            <div className="flex h-16 items-center justify-between px-4 border-b border-border">
              <Link to="/chat" className="flex items-center gap-2">
                <Hash className="h-8 w-8 text-primary" />
                <span className="font-bold text-xl">TeamUp</span>
              </Link>
            </div>

            {/* Channels list */}
            <div className="flex-1 overflow-y-auto p-2 space-y-1">
              {channels.map((channel) => (
                <button
                  key={channel.id}
                  onClick={() => handleChannelClick(channel.id)}
                  className={cn(
                    'w-full flex items-center gap-3 rounded-lg px-3 py-2 text-left text-sm font-medium transition-colors',
                    channel.id === channelId
                      ? 'bg-primary text-primary-foreground'
                      : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
                  )}
                >
                  <MessageSquare className="h-5 w-5 flex-shrink-0" />
                  <span className="truncate flex-1">{channel.name || 'Unnamed'}</span>
                  {channel.unreadCount > 0 && (
                    <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-primary/20 px-1.5 text-xs font-medium text-primary-foreground">
                      {channel.unreadCount > 9 ? '9+' : channel.unreadCount}
                    </span>
                  )}
                </button>
              ))}
            </div>

            {/* New channel button */}
            <div className="p-2 border-t border-border">
              <Button variant="outline" className="w-full" onClick={() => setCreateChannelOpen(true)}>
                <MessageSquare className="mr-2 h-4 w-4" />
                New Channel
              </Button>
            </div>
          </div>
        </aside>

        {/* Main chat area */}
        <div className="flex-1 flex flex-col min-w-0">
          {/* Channel header */}
          <div className="flex h-16 items-center gap-4 px-4 border-b border-border">
            <div className="flex items-center gap-3">
              <MessageSquare className="h-8 w-8 text-primary" />
              <div>
                <h2 className="font-semibold">
                  {activeChannel?.name || (channelId ? 'Channel' : 'No channel selected')}
                </h2>
                <p className="text-sm text-muted-foreground">
                  {channelId ? `${activeChannel?.type || 'group'} channel` : 'Pick a channel to start reading'}
                </p>
              </div>
            </div>
            <div className="flex-1" />
            <div className="flex items-center gap-2">
              <Button variant="ghost" size="icon"><Video className="h-5 w-5" /></Button>
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
          <div className="flex-1 overflow-y-auto p-4 space-y-4">
            {messagesLoading ? (
              <div className="flex items-center justify-center h-full">Loading messages...</div>
            ) : messages.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-full text-muted-foreground">
                <MessageSquare className="h-12 w-12 mb-4 opacity-50" />
                <p className="text-lg">No messages yet</p>
                <p className="text-sm">Start the conversation!</p>
              </div>
            ) : (
              <>
                {messages.map((msg) => (
                  <MessageBubble key={msg.id} message={msg} isOwn={msg.userId === user?.id} status={statusOf(msg.userId)} />
                ))}
                <div ref={messagesEndRef} />
              </>
            )}
          </div>

          {/* Message input */}
          {attachments.length > 0 && (
            <div className="border-t border-border px-4 py-2">
              <p className="mb-1 text-xs font-medium text-muted-foreground">Files in this channel</p>
              <div className="flex flex-wrap gap-2">
                {attachments.map((a: any) => (
                  <AttachmentChip key={a.id} attachment={a} />
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
                  if (uploaded) {
                    queryClient.invalidateQueries({ queryKey: ['attachments', channelId] });
                    toast.success(`${uploaded.filename} attached.`);
                  }
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
                  placeholder="Type a message..."
                  className="pr-10"
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault();
                      handleSend(e);
                    }
                  }}
                />
                <div className="absolute right-2 bottom-2 flex items-center gap-1">
                  <Button variant="ghost" size="icon" type="button"><Smile className="h-5 w-5" /></Button>
                </div>
              </div>
              <Button type="submit" size="icon" disabled={!message.trim()} className="shrink-0">
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

      <ChannelSettingsDialog
        channel={showSettings ? activeChannel ?? null : null}
        onOpenChange={(open) => !open && setShowSettings(false)}
      />
    </div>
  );
}

function MessageBubble({
  message,
  isOwn,
  status,
}: {
  message: Message;
  isOwn: boolean;
  status: PresenceStatus;
}) {
  return (
    <div className={cn('flex gap-3 max-w-[70%]', isOwn ? 'justify-end' : 'justify-start')}>
      {!isOwn && (
        <div className="relative shrink-0">
          <Avatar className="h-8 w-8">
            <AvatarFallback>{message.userName.charAt(0).toUpperCase()}</AvatarFallback>
          </Avatar>
          <PresenceDot status={status} className="absolute -bottom-0.5 -right-0.5" />
        </div>
      )}
      <div className={cn('flex flex-col', isOwn ? 'items-end' : 'items-start')}>
        {!isOwn && <p className="text-xs text-muted-foreground mb-1">{message.userName}</p>}
        <div className={cn(
          'rounded-2xl px-4 py-2 max-w-[80%]',
          isOwn ? 'bg-primary text-primary-foreground rounded-tr-none' : 'bg-secondary text-secondary-foreground rounded-tl-none'
        )}>
          <p className="whitespace-pre-wrap">{message.content}</p>
          <p className={cn('text-xs mt-1 opacity-70', isOwn ? 'text-primary-foreground/70' : 'text-muted-foreground')}>
            {formatRelativeTime(message.createdAt)}
          </p>
        </div>
      </div>
    </div>
  );
}
