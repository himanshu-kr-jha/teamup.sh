import type { QueryClient } from '@tanstack/react-query';
import type { Message as Frame, WSMessageType } from '@/shared/types/protocol';

/** An edit arrives as a partial chat.message with no author. See ticket 31. */
export function isEditFrame(frame: { userId?: string }): boolean {
  return !frame.userId;
}

export function applyEdit<T>(message: T, edit: { content: string; editedAt?: string }): T {
  return { ...message, content: edit.content, editedAt: edit.editedAt };
}

export function applyDelete<T>(message: T, at: string): T {
  return { ...message, deletedAt: at, content: '', attachments: [] };
}

type Subscribe = (type: WSMessageType, handler: (msg: Frame) => void) => () => void;

/**
 * Keeps what is cached right for screens that are not open. The chat page patches
 * what is on screen and always re-reads a conversation when opened, but a closed
 * thread would otherwise show an edited or deleted message from cache for minutes.
 * Stale, not refetched: nothing is fetched until somebody looks.
 */
export function watchChatChanges(on: Subscribe, client: QueryClient): () => void {
  const threadsChanged = () => {
    client.invalidateQueries({ queryKey: ['thread'], refetchType: 'none' });
  };
  const unsubscribers = [
    on('chat.membership.changed', () => {
      client.invalidateQueries({ queryKey: ['channels'] });
    }),
    on('chat.delete', threadsChanged),
    on('chat.message', (msg) => {
      const payload = msg.payload as { message?: { userId?: string } };
      if (payload?.message && isEditFrame(payload.message)) threadsChanged();
    }),
  ];
  return () => unsubscribers.forEach((unsubscribe) => unsubscribe());
}
