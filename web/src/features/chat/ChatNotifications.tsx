import { useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { plainText } from '@/shared/richText/format';
import { useQueryClient } from '@tanstack/react-query';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useAuth } from '@/features/auth/AuthProvider';
import { useChime } from '@/shared/hooks/useChime';
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';
import { isEditFrame, watchChatChanges } from './liveChanges';
import type { NotifyLevel } from '@/shared/api/client';
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

      // What this person chose to hear about from this conversation (the bell in
      // its header). The unread count below is kept whatever they chose.
      const channel = ((queryClient.getQueryData(['channels']) as any)?.data ?? []).find(
        (c: any) => c.id === incoming.channelId
      );
      const level: NotifyLevel = channel?.notifyLevel ?? 'all';
      const interrupt =
        level === 'all' ||
        (level === 'mentions' && !!user && mentionsPerson(incoming.content ?? '', { id: user.id, name: user.name ?? '', email: user.email }));

      if (interrupt) ping();

      const watchingThisConversation =
        document.visibilityState === 'visible' &&
        pathnameRef.current === `/chat/${incoming.channelId}`;

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
      // sound and the notification below still happen — you should hear that
      // somebody answered — and the way to see it is the reply count on the
      // parent, which the chat page moves as the frame arrives.
      const known = ((queryClient.getQueryData(['channels']) as any)?.data ?? []).some(
        (c: any) => c.id === incoming.channelId
      );
      if (!known) {
        // A conversation this list has never seen, such as somebody opening
        // a direct message with you: there is nothing to add one to, so the
        // list is read again and arrives with it, unread count and all.
        void queryClient.invalidateQueries({ queryKey: ['channels'] });
      } else if (!watchingThisConversation && !incoming.replyTo) {
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

      if (watchingThisConversation || !interrupt) return;

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
    });
  }, [on, user, queryClient, navigate, ping, notify]);

  return null;
}
