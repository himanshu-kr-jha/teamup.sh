// Installing TeamUp as an app on this device: on its home screen or in its app
// list, opening in a window of its own, and on an iPhone the only way it can
// show notifications at all.
//
// Chrome, Edge and Samsung Internet offer to install with an event, which is
// caught here as early as possible, kept, and used when somebody asks. Safari
// on an iPhone has no such offer: there it is done from the Share menu, and the
// page can only say how.

import { useSyncExternalStore } from 'react';

/** The browser's offer to install, which is not in TypeScript's DOM types yet. */
interface InstallPromptEvent extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}

export type InstallWay =
  /** Already installed, or this is the installed app. */
  | 'installed'
  /** The browser will install it at a click. */
  | 'prompt'
  /** An iPhone or iPad: from the Share menu, by hand. */
  | 'ios'
  /** This browser does not say; its own menu may still offer it. */
  | 'unknown';

let offer: InstallPromptEvent | null = null;
let installed = false;
const listeners = new Set<() => void>();
const changed = () => listeners.forEach((l) => l());

/** Whether this page is inside the TeamUp phone app (mobile/), which adds this to its user agent. */
export function isPhoneApp(): boolean {
  return typeof navigator !== 'undefined' && navigator.userAgent.includes('TeamUpApp');
}

/** Whether this page is the installed app rather than a tab. */
export function isStandalone(): boolean {
  if (isPhoneApp()) return true;
  try {
    return matchMedia('(display-mode: standalone)').matches || (navigator as Navigator & { standalone?: boolean }).standalone === true;
  } catch {
    return false;
  }
}

/** An iPhone or iPad, whose iPadOS also calls itself a Mac. */
export function isIOS(): boolean {
  const ua = navigator.userAgent;
  return /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
}

// Installed from a tab, the tab stays a tab and the browser never says so again;
// remembered here. A browser offering to install again means it was removed.
const INSTALLED_KEY = 'teamup.install.done';
function remember(done: boolean) {
  try {
    if (done) localStorage.setItem(INSTALLED_KEY, '1');
    else localStorage.removeItem(INSTALLED_KEY);
  } catch {
    // Private browsing: known for this visit only.
  }
}
function remembered(): boolean {
  try {
    return localStorage.getItem(INSTALLED_KEY) === '1';
  } catch {
    return false;
  }
}

let watching = false;
/** Starts listening for the browser's offer. Called before the app renders, as the offer can come early. */
export function watchInstall() {
  if (watching || typeof window === 'undefined') return;
  watching = true;
  installed = isStandalone() || remembered();
  window.addEventListener('beforeinstallprompt', (e) => {
    // Kept for when somebody asks, instead of the browser's own bar, which
    // would cover the bottom navigation.
    e.preventDefault();
    offer = e as InstallPromptEvent;
    installed = isStandalone();
    remember(false);
    changed();
  });
  window.addEventListener('appinstalled', () => {
    installed = true;
    remember(true);
    offer = null;
    changed();
  });
}

function way(): InstallWay {
  if (installed) return 'installed';
  if (offer) return 'prompt';
  if (isIOS()) return 'ios';
  return 'unknown';
}

export function useInstallWay(): InstallWay {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    way,
    () => 'unknown' as InstallWay
  );
}

/** Asks the browser to install. Resolves with whether the person agreed. */
export async function install(): Promise<boolean> {
  const asked = offer;
  if (!asked) return false;
  await asked.prompt();
  const { outcome } = await asked.userChoice;
  // An offer can be used once; the browser makes a new one if it is turned down.
  offer = null;
  if (outcome === 'accepted') {
    installed = true;
    remember(true);
  }
  changed();
  return outcome === 'accepted';
}

const DISMISSED_KEY = 'teamup.install.dismissed';

/** Whether the suggestion on a phone was put away on this device. Settings still offers it. */
export function installDismissed(): boolean {
  try {
    return localStorage.getItem(DISMISSED_KEY) === '1';
  } catch {
    return false;
  }
}

export function dismissInstall() {
  try {
    localStorage.setItem(DISMISSED_KEY, '1');
  } catch {
    // Private browsing: put away for this visit only.
  }
}
