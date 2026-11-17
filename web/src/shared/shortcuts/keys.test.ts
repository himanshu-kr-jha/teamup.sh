import { describe, expect, it, vi } from 'vitest';
import {
  SEQUENCE_TIMEOUT_MS,
  comboAria,
  comboText,
  createShortcutEngine,
  detectMac,
  isTypingTarget,
  parseCombo,
  type KeyEventLike,
} from './keys';
import { SHORTCUTS, shortcutCombos, shortcutHint } from './catalogue';

const body = { tagName: 'BODY', getAttribute: () => null };
const button = { tagName: 'BUTTON', getAttribute: () => null };
const textarea = { tagName: 'TEXTAREA', getAttribute: () => null };

function key(k: string, mods: Partial<KeyEventLike> = {}): KeyEventLike & { prevented: boolean } {
  const e = {
    key: k,
    code: /^[a-z]$/i.test(k) ? `Key${k.toUpperCase()}` : k === ' ' ? 'Space' : undefined,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    target: body as unknown as EventTarget,
    prevented: false,
    preventDefault() {
      e.prevented = true;
    },
    ...mods,
  };
  return e;
}

function setup({ mac = false, singleKeys = true, dialog = false } = {}) {
  let clock = 0;
  const state = { singleKeys, dialog };
  const engine = createShortcutEngine({
    mac,
    singleKeys: () => state.singleKeys,
    dialogOpen: () => state.dialog,
    now: () => clock,
  });
  return {
    engine,
    state,
    advance: (ms: number) => {
      clock += ms;
    },
    bind: (keys: string, handler = vi.fn(), extra: { priority?: number; inDialogs?: boolean; release?: () => void } = {}) => {
      const off = engine.register({ id: keys, combos: [parseCombo(keys)], handler, ...extra });
      return { handler, off };
    },
  };
}

describe('parsing and showing keys', () => {
  it('reads chords, single keys and sequences', () => {
    expect(parseCombo('mod+shift+s')).toEqual({ kind: 'chord', mod: true, shift: true, alt: false, key: 's' });
    expect(parseCombo('alt+arrowdown')).toEqual({ kind: 'chord', mod: false, shift: false, alt: true, key: 'arrowdown' });
    expect(parseCombo('?')).toEqual({ kind: 'single', key: '?' });
    expect(parseCombo('g t')).toEqual({ kind: 'sequence', keys: ['g', 't'] });
  });

  it('says Cmd on a Mac and Ctrl elsewhere', () => {
    expect(comboText(parseCombo('mod+d'), false)).toBe('Ctrl+D');
    expect(comboText(parseCombo('mod+d'), true)).toBe('Cmd+D');
    expect(comboText(parseCombo('mod+shift+s'), true)).toBe('Cmd+Shift+S');
    expect(comboText(parseCombo('alt+arrowup'), true)).toBe('Option+↑');
    expect(comboText(parseCombo('g t'), false)).toBe('G then T');
  });

  it('gives aria-keyshortcuts the platform key, and nothing for a sequence', () => {
    expect(comboAria(parseCombo('mod+d'), false)).toBe('Control+D');
    expect(comboAria(parseCombo('mod+d'), true)).toBe('Meta+D');
    expect(comboAria(parseCombo('space'), false)).toBe('Space');
    expect(comboAria(parseCombo('g t'), false)).toBeNull();
    expect(shortcutHint('call.mic', 'Turn your microphone off', true)).toEqual({
      title: 'Turn your microphone off (Cmd+D)',
      'aria-keyshortcuts': 'Meta+D',
    });
    expect(shortcutHint('help', 'Shortcuts', false)['aria-keyshortcuts']).toBe('? Control+/');
  });

  it('knows a Mac when it sees one', () => {
    expect(detectMac('MacIntel')).toBe(true);
    expect(detectMac('iPhone')).toBe(true);
    expect(detectMac('Win32')).toBe(false);
    expect(detectMac('Linux x86_64')).toBe(false);
  });

  it('has a well-formed catalogue: unique ids, and every key parses', () => {
    expect(new Set(SHORTCUTS.map((s) => s.id)).size).toBe(SHORTCUTS.length);
    for (const s of SHORTCUTS) expect(shortcutCombos(s.id).length).toBeGreaterThan(0);
  });
});

describe('isTypingTarget', () => {
  it('counts text boxes and editable content, not buttons or tick boxes', () => {
    expect(isTypingTarget({ tagName: 'INPUT', type: 'text' } as any)).toBe(true);
    expect(isTypingTarget({ tagName: 'INPUT', type: 'search' } as any)).toBe(true);
    expect(isTypingTarget({ tagName: 'INPUT', type: 'checkbox' } as any)).toBe(false);
    expect(isTypingTarget(textarea as any)).toBe(true);
    expect(isTypingTarget({ tagName: 'SELECT' } as any)).toBe(true);
    expect(isTypingTarget({ tagName: 'DIV', isContentEditable: true } as any)).toBe(true);
    expect(isTypingTarget({ tagName: 'DIV', getAttribute: () => 'textbox' } as any)).toBe(true);
    expect(isTypingTarget(button as any)).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
  });
});

describe('chords', () => {
  it('Ctrl+D fires on Windows and Linux, not Cmd+D', () => {
    const t = setup({ mac: false });
    const { handler } = t.bind('mod+d');
    const cmd = key('d', { metaKey: true });
    expect(t.engine.keyDown(cmd)).toBe(false);
    expect(handler).not.toHaveBeenCalled();
    const ctrl = key('d', { ctrlKey: true });
    expect(t.engine.keyDown(ctrl)).toBe(true);
    expect(handler).toHaveBeenCalledTimes(1);
    expect(ctrl.prevented).toBe(true);
  });

  it('Cmd+D fires on a Mac, not Ctrl+D', () => {
    const t = setup({ mac: true });
    const { handler } = t.bind('mod+d');
    t.engine.keyDown(key('d', { ctrlKey: true }));
    expect(handler).not.toHaveBeenCalled();
    t.engine.keyDown(key('d', { metaKey: true }));
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('needs Shift exactly as written', () => {
    const t = setup();
    const share = t.bind('mod+shift+s');
    const plain = t.bind('mod+s');
    t.engine.keyDown(key('S', { ctrlKey: true, shiftKey: true }));
    expect(share.handler).toHaveBeenCalledTimes(1);
    expect(plain.handler).not.toHaveBeenCalled();
  });

  it('works while typing, since nobody types with Ctrl held', () => {
    const t = setup();
    const { handler } = t.bind('mod+k');
    t.engine.keyDown(key('k', { ctrlKey: true, target: textarea as any }));
    expect(handler).toHaveBeenCalled();
  });

  it('leaves a key the page already used alone (Ctrl+E for code in a message box)', () => {
    const t = setup();
    const { handler } = t.bind('mod+e');
    const e = key('e', { ctrlKey: true, defaultPrevented: true });
    expect(t.engine.keyDown(e)).toBe(false);
    expect(handler).not.toHaveBeenCalled();
  });

  it('fires once while held, but keeps the repeats from the browser', () => {
    const t = setup();
    const { handler } = t.bind('mod+d');
    t.engine.keyDown(key('d', { ctrlKey: true }));
    const repeat = key('d', { ctrlKey: true, repeat: true });
    expect(t.engine.keyDown(repeat)).toBe(true);
    expect(repeat.prevented).toBe(true);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('matches the physical key when Alt changes the character', () => {
    const t = setup();
    const { handler } = t.bind('alt+d');
    t.engine.keyDown(key('∂', { altKey: true, code: 'KeyD' }));
    expect(handler).toHaveBeenCalled();
  });

  it('is not taken, and the browser keeps it, when nothing is bound', () => {
    const t = setup();
    const e = key('d', { ctrlKey: true });
    expect(t.engine.keyDown(e)).toBe(false);
    expect(e.prevented).toBe(false);
  });

  it('stays on when single keys are switched off', () => {
    const t = setup({ singleKeys: false });
    const { handler } = t.bind('mod+d');
    t.engine.keyDown(key('d', { ctrlKey: true }));
    expect(handler).toHaveBeenCalled();
  });
});

describe('single keys', () => {
  it('fire on the page', () => {
    const t = setup();
    const { handler } = t.bind('c');
    expect(t.engine.keyDown(key('c'))).toBe(true);
    expect(handler).toHaveBeenCalled();
  });

  it('never fire while typing', () => {
    const t = setup();
    const { handler } = t.bind('c');
    expect(t.engine.keyDown(key('c', { target: textarea as any }))).toBe(false);
    expect(t.engine.keyDown(key('c', { target: { tagName: 'INPUT', type: 'text' } as any }))).toBe(false);
    expect(t.engine.keyDown(key('c', { target: { tagName: 'DIV', isContentEditable: true } as any }))).toBe(false);
    expect(handler).not.toHaveBeenCalled();
  });

  it('never fire with a modifier, Shift included for letters', () => {
    const t = setup();
    const { handler } = t.bind('c');
    t.engine.keyDown(key('c', { ctrlKey: true }));
    t.engine.keyDown(key('c', { altKey: true }));
    t.engine.keyDown(key('C', { shiftKey: true }));
    expect(handler).not.toHaveBeenCalled();
  });

  it('take Shift for a character that needs it, such as ?', () => {
    const t = setup();
    const { handler } = t.bind('?');
    t.engine.keyDown(key('?', { shiftKey: true }));
    expect(handler).toHaveBeenCalled();
  });

  it('wait while a dialog is open, unless they say otherwise', () => {
    const t = setup({ dialog: true });
    const plain = t.bind('c');
    const help = t.bind('?', vi.fn(), { inDialogs: true });
    t.engine.keyDown(key('c'));
    t.engine.keyDown(key('?', { shiftKey: true }));
    expect(plain.handler).not.toHaveBeenCalled();
    expect(help.handler).toHaveBeenCalled();
  });

  it('are all off when switched off', () => {
    const t = setup({ singleKeys: false });
    const single = t.bind('c');
    const seq = t.bind('g t');
    t.engine.keyDown(key('c'));
    t.engine.keyDown(key('g'));
    t.engine.keyDown(key('t'));
    expect(single.handler).not.toHaveBeenCalled();
    expect(seq.handler).not.toHaveBeenCalled();
    t.state.singleKeys = true;
    t.engine.keyDown(key('c'));
    expect(single.handler).toHaveBeenCalled();
  });

  it('go to the page before the app: the higher priority, then the newest', () => {
    const t = setup();
    const fallback = t.bind('/', vi.fn(), { priority: -1 });
    const page = t.bind('/');
    t.engine.keyDown(key('/'));
    expect(page.handler).toHaveBeenCalledTimes(1);
    expect(fallback.handler).not.toHaveBeenCalled();
    page.off();
    t.engine.keyDown(key('/'));
    expect(fallback.handler).toHaveBeenCalledTimes(1);
  });

  it('pass on to the next binding when a handler says not now', () => {
    const t = setup();
    const fallback = t.bind('/', vi.fn(), { priority: -1 });
    const page = t.bind('/', vi.fn(() => false));
    const e = key('/');
    expect(t.engine.keyDown(e)).toBe(true);
    expect(page.handler).toHaveBeenCalled();
    expect(fallback.handler).toHaveBeenCalled();
  });

  it('let the key through when every handler says not now', () => {
    const t = setup();
    t.bind('c', vi.fn(() => false));
    const e = key('c');
    expect(t.engine.keyDown(e)).toBe(false);
    expect(e.prevented).toBe(false);
  });
});

describe('sequences', () => {
  it('G then T, within the time allowed', () => {
    const t = setup();
    const tasks = t.bind('g t');
    t.engine.keyDown(key('g'));
    t.advance(SEQUENCE_TIMEOUT_MS - 100);
    t.engine.keyDown(key('t'));
    expect(tasks.handler).toHaveBeenCalledTimes(1);
  });

  it('forgets the G after the timeout', () => {
    const t = setup();
    const tasks = t.bind('g t');
    t.engine.keyDown(key('g'));
    t.advance(SEQUENCE_TIMEOUT_MS + 1);
    t.engine.keyDown(key('t'));
    expect(tasks.handler).not.toHaveBeenCalled();
  });

  it('outranks a single key that is also its second key', () => {
    const t = setup();
    const chat = t.bind('g c');
    const newTask = t.bind('c');
    t.engine.keyDown(key('g'));
    t.engine.keyDown(key('c'));
    expect(chat.handler).toHaveBeenCalled();
    expect(newTask.handler).not.toHaveBeenCalled();
    t.engine.keyDown(key('c'));
    expect(newTask.handler).toHaveBeenCalledTimes(1);
  });

  it('is not started or finished by typing "g t" in a box', () => {
    const t = setup();
    const tasks = t.bind('g t');
    t.engine.keyDown(key('g', { target: textarea as any }));
    t.engine.keyDown(key('t'));
    expect(tasks.handler).not.toHaveBeenCalled();
    t.engine.keyDown(key('g'));
    t.engine.keyDown(key('t', { target: textarea as any }));
    t.engine.keyDown(key('t'));
    expect(tasks.handler).not.toHaveBeenCalled();
  });

  it('ignores Shift pressed on its own between the keys', () => {
    const t = setup();
    const tasks = t.bind('g t');
    t.engine.keyDown(key('g'));
    t.engine.keyDown(key('Shift', { shiftKey: true }));
    t.engine.keyDown(key('t'));
    expect(tasks.handler).toHaveBeenCalled();
  });

  it('is dropped when the window loses the focus', () => {
    const t = setup();
    const tasks = t.bind('g t');
    t.engine.keyDown(key('g'));
    t.engine.blur();
    t.engine.keyDown(key('t'));
    expect(tasks.handler).not.toHaveBeenCalled();
  });
});

describe('held keys (Space to talk)', () => {
  it('calls release when the key is let go, once', () => {
    const t = setup();
    const release = vi.fn();
    const talk = t.bind('space', vi.fn(), { release });
    const down = key(' ');
    expect(t.engine.keyDown(down)).toBe(true);
    expect(down.prevented).toBe(true);
    expect(t.engine.keyDown(key(' ', { repeat: true }))).toBe(true);
    expect(talk.handler).toHaveBeenCalledTimes(1);
    t.engine.keyUp({ key: ' ', code: 'Space' });
    t.engine.keyUp({ key: ' ', code: 'Space' });
    expect(release).toHaveBeenCalledTimes(1);
  });

  it('releases when the window loses the focus mid-hold', () => {
    const t = setup();
    const release = vi.fn();
    t.bind('space', vi.fn(), { release });
    t.engine.keyDown(key(' '));
    t.engine.blur();
    expect(release).toHaveBeenCalledTimes(1);
  });

  it('leaves Space to a focused button, which it presses', () => {
    const t = setup();
    const talk = t.bind('space', vi.fn(), { release: vi.fn() });
    expect(t.engine.keyDown(key(' ', { target: button as any }))).toBe(false);
    expect(talk.handler).not.toHaveBeenCalled();
  });

  it('is not held when the handler said not now (already unmuted)', () => {
    const t = setup();
    const release = vi.fn();
    t.bind('space', vi.fn(() => false), { release });
    expect(t.engine.keyDown(key(' '))).toBe(false);
    t.engine.keyUp({ key: ' ', code: 'Space' });
    expect(release).not.toHaveBeenCalled();
  });
});
