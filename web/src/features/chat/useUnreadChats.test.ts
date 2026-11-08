import { describe, expect, it } from 'vitest';
import { badgeText, unreadChatCount } from './useUnreadChats';

describe('the number on the Chat tab', () => {
  it('counts conversations with anything unread, not messages', () => {
    expect(unreadChatCount([{ unreadCount: 40 }, { unreadCount: 0 }, { unreadCount: 1 }, {}])).toBe(2);
  });
  it('is nothing before the list has loaded', () => {
    expect(unreadChatCount(undefined)).toBe(0);
  });
  it('stops at 9+', () => {
    expect(badgeText(3)).toBe('3');
    expect(badgeText(10)).toBe('9+');
  });
});
