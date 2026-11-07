import { useSyncExternalStore } from 'react';

/**
 * Whether a saved choice means dark. Nothing saved means the same as System.
 *
 * The inline script in index.html applies this same rule before the first paint,
 * where it cannot import anything. theme.test.ts keeps the two in step.
 */
export function isDarkTheme(saved: string | null, systemDark: boolean): boolean {
  return saved === 'dark' || (saved !== 'light' && systemDark);
}

/** Saves a choice of 'light', 'dark' or 'system' and puts it on screen. */
export function applyTheme(choice: string) {
  localStorage.setItem('theme', choice);
  const systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  document.documentElement.classList.toggle('dark', isDarkTheme(choice, systemDark));
}

function subscribeToDocumentClass(onChange: () => void) {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  return () => observer.disconnect();
}

/** The theme actually on screen, whoever set it. */
export function useIsDark(): boolean {
  return useSyncExternalStore(subscribeToDocumentClass, () => document.documentElement.classList.contains('dark'));
}
