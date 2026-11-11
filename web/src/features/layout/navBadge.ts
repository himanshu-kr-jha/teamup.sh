/** The number a section of the app wears, and how a screen reader says it. */
export interface NavBadge {
  count: number;
  testId: string;
  spoken: string;
}

/** Chat counts conversations with unread messages; Tasks, tasks with unread news. */
export function navBadge(path: string, unread: { chats: number; tasks: number }): NavBadge | null {
  if (path === '/chat' && unread.chats > 0) {
    const n = unread.chats;
    return { count: n, testId: 'chat-unread-badge', spoken: n === 1 ? '1 chat with unread messages' : `${n} chats with unread messages` };
  }
  if (path === '/tasks' && unread.tasks > 0) {
    const n = unread.tasks;
    return { count: n, testId: 'tasks-unread-badge', spoken: n === 1 ? '1 task with unread updates' : `${n} tasks with unread updates` };
  }
  return null;
}
