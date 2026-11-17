import { useSyncExternalStore } from 'react';

/**
 * Whether single-key shortcuts (C, G then T, Space to talk) are on. Some people
 * press keys by accident, or use a screen reader with keys of its own, so they
 * can be turned off; Ctrl and Cmd shortcuts stay on regardless. Kept on this
 * device only: a keyboard habit belongs to the machine as much as the person.
 */
const KEY = 'teamup.singleKeyShortcuts';

const listeners = new Set<() => void>();
/** The choice made in this tab, for when storage is refused. */
let chosen: boolean | null = null;

export function singleKeysOn(): boolean {
  try {
    return localStorage.getItem(KEY) !== 'off';
  } catch {
    // Storage can be refused (a private window, blocked site data); this
    // tab's own choice, or the default, is the answer then.
    return chosen ?? true;
  }
}

export function setSingleKeys(on: boolean) {
  chosen = on;
  try {
    if (on) localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, 'off');
  } catch {
    // Not remembered, but nothing else breaks.
  }
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  // Another tab changing it counts too.
  const onStorage = (e: StorageEvent) => {
    if (e.key === KEY) listener();
  };
  window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('storage', onStorage);
  };
}

export function useSingleKeys(): boolean {
  return useSyncExternalStore(subscribe, singleKeysOn, () => true);
}
