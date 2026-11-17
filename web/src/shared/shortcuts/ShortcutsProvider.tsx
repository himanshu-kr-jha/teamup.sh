import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { create } from 'zustand';
import { IS_MAC, createShortcutEngine, type KeyEventLike, type ShortcutEngine } from './keys';
import { shortcutCombos, type ShortcutId } from './catalogue';
import { singleKeysOn } from './preference';

const EngineContext = createContext<ShortcutEngine | null>(null);

/**
 * Something over the page that single keys must not act behind. One already
 * closing, still fading out, does not count: a key pressed just after picking
 * from the palette is meant for the page it opened.
 */
export function overlayOpen(): boolean {
  return !!document.querySelector(
    ['dialog', 'alertdialog', 'menu', 'listbox'].map((role) => `[role="${role}"]:not([data-state="closed"])`).join(', ')
  );
}

/**
 * The one listener for every shortcut on the page. Components say what they
 * want with useShortcut; this decides which of them a key goes to.
 *
 * It listens on the window, after the page: whatever is focused has already
 * had the key, and a message box that used Ctrl+E for code keeps it.
 */
export function ShortcutsProvider({ children }: { children: ReactNode }) {
  const [engine] = useState(() =>
    createShortcutEngine({ mac: IS_MAC, singleKeys: singleKeysOn, dialogOpen: overlayOpen })
  );
  useEffect(() => {
    const down = (e: KeyboardEvent) => engine.keyDown(e);
    const up = (e: KeyboardEvent) => engine.keyUp(e);
    const blur = () => engine.blur();
    window.addEventListener('keydown', down);
    window.addEventListener('keyup', up);
    window.addEventListener('blur', blur);
    return () => {
      window.removeEventListener('keydown', down);
      window.removeEventListener('keyup', up);
      window.removeEventListener('blur', blur);
    };
  }, [engine]);
  return <EngineContext.Provider value={engine}>{children}</EngineContext.Provider>;
}

interface ShortcutOptions {
  /** Off while false; the key then does whatever it would have done. */
  enabled?: boolean;
  /** A held key's other half: called when it is let go. */
  release?: () => void;
  /** Higher wins; a page's own search outranks the header's. */
  priority?: number;
  /** Fire even while a dialog is open. */
  inDialogs?: boolean;
}

/**
 * Binds a shortcut from the catalogue to what it should do here, for as long
 * as the component is mounted. Return false from the handler to let the key
 * pass, for when it means nothing at the moment.
 */
export function useShortcut(id: ShortcutId, handler: (e: KeyEventLike) => boolean | void, options: ShortcutOptions = {}) {
  const engine = useContext(EngineContext);
  const handlerRef = useRef(handler);
  const releaseRef = useRef(options.release);
  useEffect(() => {
    handlerRef.current = handler;
    releaseRef.current = options.release;
  });
  const { enabled = true, priority = 0, inDialogs = false } = options;
  const holds = !!options.release;
  useEffect(() => {
    if (!engine || !enabled) return;
    return engine.register({
      id,
      combos: shortcutCombos(id),
      handler: (e) => handlerRef.current(e),
      release: holds ? () => releaseRef.current?.() : undefined,
      priority,
      inDialogs,
    });
  }, [engine, id, enabled, priority, inDialogs, holds]);
}

/** The cheat sheet and the palette, which a menu item can open as well as a key. */
interface Panels {
  help: boolean;
  palette: boolean;
  setHelp: (open: boolean) => void;
  setPalette: (open: boolean) => void;
}

export const useShortcutPanels = create<Panels>((set) => ({
  help: false,
  palette: false,
  setHelp: (help) => set({ help, palette: false }),
  setPalette: (palette) => set({ palette, help: false }),
}));
