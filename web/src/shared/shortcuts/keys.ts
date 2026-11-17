/**
 * Keyboard shortcuts: what a key written as "mod+shift+s" or "g t" means, how
 * it is shown, and the one engine every shortcut in the app goes through.
 *
 * Nothing here touches React or the DOM's globals, so the rules (which keys
 * fire while typing, how long "G" waits for its second key, what the switch
 * in Settings turns off) are tested on their own.
 */

/** One way of pressing a shortcut. */
export type Combo =
  /** A key held with Ctrl or Cmd, Alt or Shift. Works while typing. */
  | { kind: 'chord'; mod: boolean; shift: boolean; alt: boolean; key: string }
  /** One key on its own, such as C or ?. Never while typing. */
  | { kind: 'single'; key: string }
  /** One key and then another, such as G then T. Never while typing. */
  | { kind: 'sequence'; keys: [string, string] };

/** How long the second key of a sequence may come after the first. */
export const SEQUENCE_TIMEOUT_MS = 1500;

export function parseCombo(text: string): Combo {
  const steps = text.trim().toLowerCase().split(/\s+/);
  if (steps.length === 2) return { kind: 'sequence', keys: [steps[0], steps[1]] };
  const parts = steps[0].split('+');
  // "+" itself is never a shortcut here, so splitting on it is safe.
  const key = parts[parts.length - 1];
  const mods = new Set(parts.slice(0, -1));
  if (mods.size === 0) return { kind: 'single', key };
  return { kind: 'chord', mod: mods.has('mod'), shift: mods.has('shift'), alt: mods.has('alt'), key };
}

export function detectMac(platform: string | undefined): boolean {
  return /mac|iphone|ipad|ipod/i.test(platform ?? '');
}

/** Whether this browser is on a Mac, where Cmd stands in for Ctrl. */
export const IS_MAC = typeof navigator !== 'undefined' && detectMac(navigator.platform || navigator.userAgent);

const NAMES: Record<string, string> = {
  arrowup: '↑',
  arrowdown: '↓',
  arrowleft: '←',
  arrowright: '→',
  escape: 'Esc',
  space: 'Space',
  enter: 'Enter',
};

function keyName(key: string): string {
  return NAMES[key] ?? (key.length === 1 ? key.toUpperCase() : key[0].toUpperCase() + key.slice(1));
}

/**
 * The keys to show, step by step: [["Ctrl", "D"]] for a chord,
 * [["G"], ["T"]] for a sequence.
 */
export function comboKeys(combo: Combo, mac: boolean): string[][] {
  if (combo.kind === 'sequence') return combo.keys.map((k) => [keyName(k)]);
  if (combo.kind === 'single') return [[keyName(combo.key)]];
  const keys: string[] = [];
  if (combo.mod) keys.push(mac ? 'Cmd' : 'Ctrl');
  if (combo.alt) keys.push(mac ? 'Option' : 'Alt');
  if (combo.shift) keys.push('Shift');
  keys.push(keyName(combo.key));
  return [keys];
}

/** The same in words, for a tooltip: "Ctrl+D", "G then T". */
export function comboText(combo: Combo, mac: boolean): string {
  return comboKeys(combo, mac)
    .map((step) => step.join('+'))
    .join(' then ');
}

const ARIA_KEYS: Record<string, string> = {
  arrowup: 'ArrowUp',
  arrowdown: 'ArrowDown',
  escape: 'Escape',
  space: 'Space',
};

/**
 * The value for aria-keyshortcuts, or null. ARIA has no way to say "one key
 * then another", so a sequence is left to the visible hint.
 */
export function comboAria(combo: Combo, mac: boolean): string | null {
  if (combo.kind === 'sequence') return null;
  const key = ARIA_KEYS[combo.key] ?? (combo.key.length === 1 ? combo.key.toUpperCase() : combo.key);
  if (combo.kind === 'single') return key;
  const parts: string[] = [];
  if (combo.mod) parts.push(mac ? 'Meta' : 'Control');
  if (combo.alt) parts.push('Alt');
  if (combo.shift) parts.push('Shift');
  parts.push(key);
  return parts.join('+');
}

/** The parts of a keyboard event the engine reads; a real KeyboardEvent is one. */
export interface KeyEventLike {
  key: string;
  code?: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  repeat?: boolean;
  defaultPrevented?: boolean;
  target: EventTarget | null;
  preventDefault: () => void;
  getModifierState?: (key: string) => boolean;
}

/** The parts of an element the engine reads. */
interface ElementLike {
  tagName?: string;
  isContentEditable?: boolean;
  type?: string;
  getAttribute?: (name: string) => string | null;
}

const NOT_TYPING_INPUTS = new Set(['button', 'checkbox', 'radio', 'range', 'color', 'file', 'submit', 'reset', 'image']);

/** Whether a key pressed here is somebody writing, which no single-key shortcut may take. */
export function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as ElementLike | null;
  if (!el || !el.tagName) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName.toUpperCase();
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if (tag === 'INPUT') return !NOT_TYPING_INPUTS.has((el.type ?? 'text').toLowerCase());
  const role = el.getAttribute?.('role');
  return role === 'textbox' || role === 'combobox' || role === 'searchbox';
}

/** Whether Space here already means something: pressing a button, ticking a box. */
function spaceIsTaken(target: EventTarget | null): boolean {
  const el = target as ElementLike | null;
  if (!el || !el.tagName) return false;
  const tag = el.tagName.toUpperCase();
  if (tag === 'BUTTON' || tag === 'A' || tag === 'INPUT' || tag === 'SUMMARY') return true;
  const role = el.getAttribute?.('role');
  return !!role && ['button', 'checkbox', 'switch', 'radio', 'tab', 'menuitem', 'option', 'link', 'slider'].includes(role);
}

/** The event's key, in the lower-case names combos are written in. */
function eventKey(e: KeyEventLike): string {
  if (e.key === ' ' || e.code === 'Space') return 'space';
  return e.key.toLowerCase();
}

const PLAIN = /^[a-z0-9]$/;

export function chordMatches(combo: Combo & { kind: 'chord' }, e: KeyEventLike, mac: boolean): boolean {
  // Ctrl on a Mac and Cmd elsewhere are different keys there, and each has
  // jobs of its own, so only the one this platform calls "mod" counts.
  const modDown = mac ? e.metaKey : e.ctrlKey;
  const otherDown = mac ? e.ctrlKey : e.metaKey;
  if (combo.mod !== modDown || otherDown) return false;
  if (combo.alt !== e.altKey || combo.shift !== e.shiftKey) return false;
  // AltGr reports itself as Ctrl+Alt on Windows, and is how a Polish or
  // German keyboard types letters such as ć: that is writing, not a shortcut.
  if (combo.alt && e.getModifierState?.('AltGraph')) return false;
  const key = eventKey(e);
  if (key === combo.key) return true;
  // With Alt or Shift held some layouts change the character a letter types
  // (Option+D is a symbol on a Mac); the physical key is still the letter.
  return PLAIN.test(combo.key) && e.code === (/[0-9]/.test(combo.key) ? `Digit${combo.key}` : `Key${combo.key.toUpperCase()}`);
}

function singleMatches(key: string, e: KeyEventLike): boolean {
  if (eventKey(e) !== key) return false;
  // Shift is part of typing "?", but Shift with a letter is not the letter.
  return PLAIN.test(key) || key === 'space' ? !e.shiftKey : true;
}

export interface Binding {
  id: string;
  combos: Combo[];
  /** Return false to say "not now", and let the key go on to whatever else wants it. */
  handler: (e: KeyEventLike) => boolean | void;
  /** For a key that is held, such as Space to talk: called when it is let go. */
  release?: () => void;
  /** A page's own shortcut outranks the app's fallback for the same keys. */
  priority?: number;
  /** Single keys normally wait while a dialog is open; this one does not. */
  inDialogs?: boolean;
}

export interface EngineOptions {
  mac: boolean;
  /** Whether single keys and sequences are on (the switch in Settings). */
  singleKeys: () => boolean;
  /** Whether a dialog, menu or list is open over the page. */
  dialogOpen: () => boolean;
  now?: () => number;
}

interface Registered extends Binding {
  order: number;
}

export function createShortcutEngine(options: EngineOptions) {
  const now = options.now ?? (() => Date.now());
  const bindings = new Set<Registered>();
  let counter = 0;
  let pending: { key: string; at: number } | null = null;
  let held: { key: string; binding: Registered } | null = null;

  const ranked = () =>
    [...bindings].sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || b.order - a.order);

  /** Tries each binding that wants this key, best first, until one takes it. */
  const run = (candidates: Registered[], e: KeyEventLike): Registered | null => {
    for (const binding of candidates) {
      if (binding.handler(e) !== false) return binding;
    }
    return null;
  };

  const register = (binding: Binding): (() => void) => {
    const entry: Registered = { ...binding, order: ++counter };
    bindings.add(entry);
    return () => {
      bindings.delete(entry);
      if (held?.binding === entry) held = null;
    };
  };

  const letGo = () => {
    const was = held;
    held = null;
    was?.binding.release?.();
  };

  /** True when a shortcut took the key, which has then been kept from the page. */
  const keyDown = (e: KeyEventLike): boolean => {
    // Something nearer the key already used it: a message box making text
    // bold with Ctrl+B, or code with Ctrl+E.
    if (e.defaultPrevented) return false;
    if (e.key === 'Shift' || e.key === 'Control' || e.key === 'Meta' || e.key === 'Alt') return false;
    const all = ranked();

    // Held keys auto-repeat; a shortcut fires once per press. Its key is still
    // kept from the page, or Ctrl+D held down would end up bookmarking.
    const chords = all.filter((b) => b.combos.some((c) => c.kind === 'chord' && chordMatches(c, e, options.mac)));
    if (chords.length > 0) {
      if (e.repeat) {
        e.preventDefault();
        return true;
      }
      pending = null;
      if (run(chords, e)) {
        e.preventDefault();
        return true;
      }
      return false;
    }

    if (e.ctrlKey || e.metaKey || e.altKey) return false;
    if (!options.singleKeys()) {
      pending = null;
      return false;
    }
    if (isTypingTarget(e.target)) {
      pending = null;
      return false;
    }
    const key = eventKey(e);
    if (e.repeat) return held?.key === key;
    const dialog = options.dialogOpen();
    const allowed = (b: Registered) => !dialog || b.inDialogs;

    // The second key of a sequence: G, then T.
    if (pending) {
      const first = pending.key;
      const fresh = now() - pending.at <= SEQUENCE_TIMEOUT_MS;
      pending = null;
      if (fresh) {
        const seq = all.filter(
          (b) => allowed(b) && b.combos.some((c) => c.kind === 'sequence' && c.keys[0] === first && c.keys[1] === key && !e.shiftKey)
        );
        if (run(seq, e)) {
          e.preventDefault();
          return true;
        }
      }
    }

    // The first key of one. It waits for the next rather than doing anything.
    if (!e.shiftKey && all.some((b) => allowed(b) && b.combos.some((c) => c.kind === 'sequence' && c.keys[0] === key))) {
      pending = { key, at: now() };
      return true;
    }

    const singles = all.filter((b) => allowed(b) && b.combos.some((c) => c.kind === 'single' && singleMatches(c.key, e)));
    if (singles.length === 0) return false;
    if (key === 'space' && spaceIsTaken(e.target)) return false;
    const took = run(singles, e);
    if (!took) return false;
    e.preventDefault();
    if (took.release) held = { key, binding: took };
    return true;
  };

  const keyUp = (e: Pick<KeyEventLike, 'key' | 'code'>) => {
    if (held && eventKey(e as KeyEventLike) === held.key) letGo();
  };

  return {
    register,
    keyDown,
    keyUp,
    /** The window lost the focus: nothing is held any more, and nothing waits. */
    blur: () => {
      pending = null;
      letGo();
    },
  };
}

export type ShortcutEngine = ReturnType<typeof createShortcutEngine>;
