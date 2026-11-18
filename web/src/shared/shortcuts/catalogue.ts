import { IS_MAC, comboAria, comboText, parseCombo, type Combo } from './keys';

/**
 * Every shortcut in the app, in one list: the cheat sheet is drawn from it,
 * tooltips read their keys from it, and a component binds a handler to an id
 * here rather than writing its keys out itself, so no two places can disagree
 * about what a key does.
 */
export type ShortcutId =
  | 'palette'
  | 'help'
  | 'search'
  | 'close'
  | 'go.chat'
  | 'go.tasks'
  | 'go.calendar'
  | 'go.notes'
  | 'go.projects'
  | 'go.organization'
  | 'go.settings'
  | 'call.mic'
  | 'call.camera'
  | 'call.share'
  | 'call.chat'
  | 'call.hand'
  | 'call.talk'
  | 'task.new'
  | 'task.edit'
  | 'task.assign'
  | 'task.status'
  | 'task.comment'
  | 'chat.previous'
  | 'chat.next'
  | 'chat.nextUnread'
  | 'chat.previousUnread';

export type ShortcutSection = 'Anywhere' | 'Go to' | 'In a call' | 'Tasks' | 'Chat';

export interface ShortcutInfo {
  id: ShortcutId;
  section: ShortcutSection;
  label: string;
  /** Alternatives; the first is the one tooltips show. */
  keys: string[];
}

export const SHORTCUTS: ShortcutInfo[] = [
  { id: 'palette', section: 'Anywhere', label: 'Search people, channels, tasks and pages', keys: ['mod+k'] },
  { id: 'help', section: 'Anywhere', label: 'Show keyboard shortcuts', keys: ['?', 'mod+/'] },
  { id: 'search', section: 'Anywhere', label: 'Search on this page', keys: ['/'] },
  { id: 'close', section: 'Anywhere', label: 'Close a dialog, menu or panel', keys: ['escape'] },

  { id: 'go.chat', section: 'Go to', label: 'Chat', keys: ['g c'] },
  { id: 'go.tasks', section: 'Go to', label: 'Tasks', keys: ['g t'] },
  { id: 'go.calendar', section: 'Go to', label: 'Calendar', keys: ['g k'] },
  { id: 'go.notes', section: 'Go to', label: 'Meeting notes', keys: ['g m'] },
  { id: 'go.projects', section: 'Go to', label: 'Projects', keys: ['g p'] },
  { id: 'go.organization', section: 'Go to', label: 'Organization', keys: ['g o'] },
  { id: 'go.settings', section: 'Go to', label: 'Settings', keys: ['g s'] },

  { id: 'call.mic', section: 'In a call', label: 'Turn your microphone on or off', keys: ['mod+d'] },
  { id: 'call.camera', section: 'In a call', label: 'Turn your camera on or off', keys: ['mod+e'] },
  { id: 'call.share', section: 'In a call', label: 'Share your screen, or stop', keys: ['mod+shift+s'] },
  { id: 'call.chat', section: 'In a call', label: 'Open or close the meeting chat', keys: ['mod+alt+c'] },
  { id: 'call.hand', section: 'In a call', label: 'Raise or lower your hand', keys: ['mod+shift+h'] },
  { id: 'call.talk', section: 'In a call', label: 'Hold to talk while muted', keys: ['space'] },

  { id: 'task.new', section: 'Tasks', label: 'New task', keys: ['c'] },
  { id: 'task.edit', section: 'Tasks', label: 'Edit the task', keys: ['e'] },
  { id: 'task.assign', section: 'Tasks', label: 'Assign the task to me', keys: ['a'] },
  { id: 'task.status', section: 'Tasks', label: 'Change the status', keys: ['s'] },
  { id: 'task.comment', section: 'Tasks', label: 'Write a comment', keys: ['m'] },

  { id: 'chat.previous', section: 'Chat', label: 'Previous conversation', keys: ['alt+arrowup'] },
  { id: 'chat.next', section: 'Chat', label: 'Next conversation', keys: ['alt+arrowdown'] },
  { id: 'chat.previousUnread', section: 'Chat', label: 'Previous unread conversation', keys: ['alt+shift+arrowup'] },
  { id: 'chat.nextUnread', section: 'Chat', label: 'Next unread conversation', keys: ['alt+shift+arrowdown'] },
];

export const SECTIONS: { name: ShortcutSection; note?: string }[] = [
  { name: 'Anywhere' },
  { name: 'Go to', note: 'Press G, then the letter.' },
  { name: 'In a call', note: 'While a call is open, here or in its corner window.' },
  { name: 'Tasks', note: 'New task on the task pages; the rest on a task.' },
  { name: 'Chat' },
];

const BY_ID = new Map(SHORTCUTS.map((s) => [s.id, s]));

export function shortcutInfo(id: ShortcutId): ShortcutInfo {
  return BY_ID.get(id)!;
}

export function shortcutCombos(id: ShortcutId): Combo[] {
  return shortcutInfo(id).keys.map(parseCombo);
}

/** "Ctrl+D", or "Cmd+D" on a Mac: the keys as a tooltip says them. */
export function shortcutText(id: ShortcutId, mac = IS_MAC): string {
  return comboText(parseCombo(shortcutInfo(id).keys[0]), mac);
}

/**
 * What a button with a shortcut carries: its tooltip with the keys added, and
 * aria-keyshortcuts so a screen reader can say them too.
 */
export function shortcutHint(id: ShortcutId, label: string, mac = IS_MAC) {
  const aria = shortcutInfo(id)
    .keys.map((k) => comboAria(parseCombo(k), mac))
    .filter((k): k is string => !!k);
  return {
    title: `${label} (${shortcutText(id, mac)})`,
    'aria-keyshortcuts': aria.length > 0 ? aria.join(' ') : undefined,
  };
}

/** "Ctrl" or "Cmd", for sentences about the modifier keys. */
export const MOD_NAME = IS_MAC ? 'Cmd' : 'Ctrl';
