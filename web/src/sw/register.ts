// Service worker registration, and knowing that a newer version is ready.
//
// A new version never reloads a page by itself, because a reload drops whoever is in a
// call there. The worker waits; the page offers a reload (UpdateOffer), and only once
// nobody is in a call in that tab.
//
// This is the browser's API rather than the plugin's registerSW. Its prompt mode also
// reloads a page when a worker takes over, and accepting in one tab does that to every
// tab, including the one in a call.

/** How often a tab left open asks whether there is a new version. The browser otherwise only asks when a page is opened. */
const CHECK_EVERY_MS = 60 * 60 * 1000;

let registration: ServiceWorkerRegistration | undefined;
let updateReady = false;
const listeners = new Set<() => void>();

function offerUpdate() {
  if (updateReady) return;
  updateReady = true;
  listeners.forEach((listener) => listener());
}

export function subscribeToUpdate(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export const isUpdateReady = () => updateReady;

/** Moves this tab to the new version. Other tabs stay as they are until they reload for themselves. */
export function applyUpdate() {
  const waiting = registration?.waiting;
  if (!waiting) {
    // Another tab has already let it take over, so all this one needs is to reload.
    window.location.reload();
    return;
  }
  // Not controllerchange: a page opened before the first worker installed has no
  // controller, and never hears it.
  waiting.addEventListener('statechange', () => {
    if (waiting.state === 'activated') window.location.reload();
  });
  waiting.postMessage({ type: 'SKIP_WAITING' });
}

async function register() {
  const url = import.meta.env.DEV ? '/dev-sw.js?dev-sw' : '/sw.js';
  try {
    registration = await navigator.serviceWorker.register(url, { scope: '/' });
  } catch (error) {
    console.error('Service worker registration failed:', error);
    return;
  }
  const current = registration;

  // Found waiting: an earlier visit fetched it and has not been replaced yet.
  if (current.waiting) offerUpdate();
  current.addEventListener('updatefound', () => {
    const installing = current.installing;
    installing?.addEventListener('statechange', () => {
      // With nothing active this is the first install, which is not a new version.
      if (installing.state === 'installed' && current.active) offerUpdate();
    });
  });
  // Another tab accepted. The worker has changed under this page, which keeps running what it loaded.
  let controlled = !!navigator.serviceWorker.controller;
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (controlled) offerUpdate();
    controlled = true;
  });

  window.setInterval(() => {
    current.update().catch(() => {
      // Offline or mid-deploy. The next check will do.
    });
  }, CHECK_EVERY_MS);
}

export function registerServiceWorker() {
  if (!('serviceWorker' in navigator)) return;
  // After load, so fetching the worker's files does not compete with the first paint.
  if (document.readyState === 'complete') void register();
  else window.addEventListener('load', () => void register(), { once: true });
}
