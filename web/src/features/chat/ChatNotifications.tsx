import { useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { plainText } from '@/shared/richText/format';
import { useQueryClient } from '@tanstack/react-query';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useAuth } from '@/features/auth/AuthProvider';
import { useChime } from '@/shared/hooks/useChime';
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';
import { isEditFrame, watchChatChanges } from './liveChanges';
import { channelsApi, type NotifyLevel } from '@/shared/api/client';
import { mentionsPerson } from './mentions';

/**
 * Watches for messages wherever you are in the app.
 *
 * This used to live on the chat page, which meant that on Tasks, Calls or
 * Settings nothing reacted to a message at all — no sound, no badge, no
 * notification. Mounted above the router, it reacts everywhere, and raises a
 * desktop notification whenever the conversation is not the one in front of
 * you: another app tab, another browser tab, or a window behind this one.
 */
export function ChatNotifications() {
  const { on } = useWebSocket();
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const { ping } = useChime();
  const { notify } = useDesktopNotifications();

  // Read inside the handler rather than captured, so resubscribing is not
  // needed every time you change page.
  const pathnameRef = useRef(location.pathname);
  pathnameRef.current = location.pathname;

  useEffect(() => watchChatChanges(on, queryClient), [on, queryClient]);

  useEffect(() => {
    return on('chat.message', (msg) => {
      const incoming = ((msg.payload as any)?.message ?? msg.payload) as {
        id?: string;
        channelId?: string;
        userId?: string;
        userName?: string;
        content?: string;
        attachments?: unknown[];
        replyTo?: string;
      };
      if (!incoming?.id || !incoming.channelId) return;
      // Somebody fixing a typo is not a new message: no sound, no badge.
      if (isEditFrame(incoming)) return;
      // Your own message coming back is not news.
      if (incoming.userId === user?.id) return;

      const watchingThisConversation =
        document.visibilityState === 'visible' &&
        pathnameRef.current === `/chat/${incoming.channelId}`;

      // What this person chose to hear about from this conversation (the bell in
      // its header). The unread count is kept whatever they chose.
      const decide = (level: NotifyLevel | null | undefined) => {
        const chosen: NotifyLevel = level ?? 'all';
        const interrupt =
          chosen === 'all' ||
          (chosen === 'mentions' && !!user && mentionsPerson(incoming.content ?? '', { id: user.id, name: user.name ?? '', email: user.email }));
        if (!interrupt) return;
        ping();
        if (watchingThisConversation) return;

        const body = incoming.content?.trim()
          ? plainText(incoming.content)
          : incoming.attachments?.length
            ? 'Sent a file'
            : 'Sent a message';

        notify({
          title: incoming.userName || 'New message',
          body,
          // One notification per conversation: a burst replaces itself rather
          // than burying the screen.
          tag: `chat-${incoming.channelId}`,
          url: `/chat/${incoming.channelId}`,
          onClick: () => navigate(`/chat/${incoming.channelId}`),
          // The server pushes every message to every device of the person's.
          alsoPushed: true,
        });
      };

      const channel = ((queryClient.getQueryData(['channels']) as any)?.data ?? []).find(
        (c: any) => c.id === incoming.channelId
      );
      if (!channel) {
        // Not in the list in hand: the list has not arrived yet, as in the moments
        // after a page loads, or this conversation is new to it, as when somebody
        // opens a direct message with you. Either way it is read now, and arrives
        // with this message already in its unread count, so nothing is added here.
        // Deciding without it took every conversation to be All messages, and a
        // conversation set to Only @mentions or Nothing chimed.
        queryClient
          .fetchQuery({ queryKey: ['channels'], queryFn: () => channelsApi.list() })
          .then((list: any) => decide(list?.data?.find((c: any) => c.id === incoming.channelId)?.notifyLevel))
          .catch(() => decide('all'));
        return;
      }

      // Unread counts live on the channel list, which is on screen only in
      // chat, so the badge has to be kept right from wherever you are.
      //
      // Counted rather than re-read: this used to refetch the entire list on
      // every message, for every person connected, which is the single
      // heaviest thing a busy conversation did — and all of it to move one
      // number by one. Somebody actually watching the conversation is not
      // counted at all; it is read as it arrives.
      //
      // A thread reply is not counted, because the server does not count one
      // either: the badge is cleared by reading the channel timeline, a reply
      // is not on it, and a badge nothing can clear sits there for ever. The
      // sound and the notification still happen — you should hear that
      // somebody answered — and the way to see it is the reply count on the
      // parent, which the chat page moves as the frame arrives.
      if (!watchingThisConversation && !incoming.replyTo) {
        queryClient.setQueryData(['channels'], (old: any) => {
          if (!old?.data) return old;
          return {
            ...old,
            data: old.data.map((c: any) =>
              c.id === incoming.channelId ? { ...c, unreadCount: (c.unreadCount ?? 0) + 1 } : c
            ),
          };
        });
      }
      decide(channel.notifyLevel);
    });
  }, [on, user, queryClient, navigate, ping, notify]);

  return null;
}
