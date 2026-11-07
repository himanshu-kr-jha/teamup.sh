import { describe, expect, it, vi } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import { applyDelete, applyEdit, isEditFrame, watchChatChanges } from './liveChanges';

const message = {
  id: 'm1',
  channelId: 'c1',
  userId: 'author',
  userName: 'Author',
  createdAt: '2026-11-01T10:00:00Z',
  content: 'wrong',
  attachments: [{ id: 'f1' }],
};

// The partial chat.message the server sends for an edit, as documented on ticket 31.
const editFrame = {
  id: 'm1',
  channelId: 'c1',
  userId: '',
  userName: '',
  content: 'right',
  createdAt: '0001-01-01T00:00:00Z',
  editedAt: '2026-11-06T14:10:20.789+05:30',
};

describe('isEditFrame', () => {
  it('tells an edit from a new message by its empty userId', () => {
    expect(isEditFrame(editFrame)).toBe(true);
    expect(isEditFrame(message)).toBe(false);
  });
});

describe('applyEdit', () => {
  it('changes the text and the edit time, and never the author or the original time', () => {
    expect(applyEdit(message, editFrame)).toEqual({
      ...message,
      content: 'right',
      editedAt: editFrame.editedAt,
    });
  });
});

describe('applyDelete', () => {
  it('takes the text and the attachments off the screen', () => {
    const gone: typeof message & { deletedAt?: string } = applyDelete(message, '2026-11-06T00:00:00Z');
    expect(gone.deletedAt).toBe('2026-11-06T00:00:00Z');
    expect(gone.content).toBe('');
    expect(gone.attachments).toEqual([]);
    expect(gone.userId).toBe('author');
  });
});

describe('watchChatChanges', () => {
  function setup() {
    const handlers = new Map<string, (msg: { payload: unknown }) => void>();
    const on = vi.fn((type: string, handler: (msg: { payload: unknown }) => void) => {
      handlers.set(type, handler);
      return () => handlers.delete(type);
    });
    const client = new QueryClient();
    client.setQueryData(['channels'], 'list');
    client.setQueryData(['thread', 'root'], 'thread');
    const stale = (key: string[]) => client.getQueryState(key)?.isInvalidated;
    const unsubscribe = watchChatChanges(on as never, client);
    return { handlers, client, stale, unsubscribe };
  }

  it('refreshes the channel list when a membership changes', () => {
    const { handlers, stale } = setup();
    handlers.get('chat.membership.changed')!({ payload: { channelId: 'c1', userIds: ['u1'], action: 'added' } });
    expect(stale(['channels'])).toBe(true);
    expect(stale(['thread', 'root'])).toBe(false);
  });

  it('marks cached threads stale on a delete, so a deleted reply is not served from cache', () => {
    const { handlers, stale } = setup();
    handlers.get('chat.delete')!({ payload: { messageId: 'm1' } });
    expect(stale(['thread', 'root'])).toBe(true);
    expect(stale(['channels'])).toBe(false);
  });

  it('marks cached threads stale on an edit, but not on an ordinary message', () => {
    const { handlers, stale } = setup();
    handlers.get('chat.message')!({ payload: { message } });
    expect(stale(['thread', 'root'])).toBe(false);
    handlers.get('chat.message')!({ payload: { message: editFrame } });
    expect(stale(['thread', 'root'])).toBe(true);
  });

  it('stops listening when asked', () => {
    const { handlers, unsubscribe } = setup();
    unsubscribe();
    expect(handlers.size).toBe(0);
  });
});
