import { useQuery } from '@tanstack/react-query';
import { channelsApi } from '@/shared/api/client';

/**
 * How many conversations have something you have not read: the number on the
 * Chat tab. Conversations, not messages, because "3" should mean three places
 * to look, whether one of them has one new message or forty.
 *
 * The same query as the chat page's channel list, so the two never disagree,
 * and ChatNotifications keeps it moving from wherever you are.
 */
export function unreadChatCount(channels: Array<{ unreadCount?: number }> | undefined): number {
  return (channels ?? []).filter((c) => (c.unreadCount ?? 0) > 0).length;
}

export function useUnreadChatCount(): number {
  const { data } = useQuery({
    queryKey: ['channels'],
    queryFn: () => channelsApi.list(),
  });
  return unreadChatCount(data?.data as Array<{ unreadCount?: number }> | undefined);
}

/** "9+" past nine, as the badges on each conversation already say. */
export function badgeText(n: number): string {
  return n > 9 ? '9+' : String(n);
}
