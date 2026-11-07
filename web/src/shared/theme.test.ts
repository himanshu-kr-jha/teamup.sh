import { describe, expect, it } from 'vitest';
import indexHtml from '../../index.html?raw';
import { isDarkTheme } from './theme';

const SAVED = ['dark', 'light', 'system', null, 'nonsense'];

describe('isDarkTheme', () => {
  it('follows an explicit choice whatever the machine prefers', () => {
    for (const systemDark of [true, false]) {
      expect(isDarkTheme('dark', systemDark)).toBe(true);
      expect(isDarkTheme('light', systemDark)).toBe(false);
    }
  });

  it('follows the machine for System, and when nothing was ever chosen', () => {
    for (const saved of ['system', null]) {
      expect(isDarkTheme(saved, true)).toBe(true);
      expect(isDarkTheme(saved, false)).toBe(false);
    }
  });
});

/** Runs the script from index.html, the only thing that runs before the first paint. */
function runStartupScript(document: unknown, localStorage: unknown, systemDark: boolean) {
  const match = indexHtml.match(/<head>[\s\S]*?<script>([\s\S]*?)<\/script>[\s\S]*?<\/head>/);
  expect(match, 'an inline script in <head>').not.toBeNull();
  const matchMedia = () => ({ matches: systemDark });
  new Function('document', 'localStorage', 'matchMedia', match![1])(document, localStorage, matchMedia);
}

function fakeDocument() {
  const classes = new Set<string>();
  return {
    classes,
    documentElement: {
      classList: {
        toggle: (name: string, on: boolean) => {
          if (on) classes.add(name);
          else classes.delete(name);
        },
      },
    },
  };
}

describe('the startup script in index.html', () => {
  it.each(SAVED.flatMap((saved) => [true, false].map((systemDark) => [saved, systemDark] as const)))(
    'agrees with isDarkTheme for saved=%s, system dark=%s',
    (saved, systemDark) => {
      const doc = fakeDocument();
      runStartupScript(doc, { getItem: () => saved }, systemDark);
      expect(doc.classes.has('dark')).toBe(isDarkTheme(saved, systemDark));
    }
  );

  it('still follows the machine when storage is blocked', () => {
    const doc = fakeDocument();
    const blocked = {
      getItem: () => {
        throw new Error('denied');
      },
    };
    runStartupScript(doc, blocked, true);
    expect(doc.classes.has('dark')).toBe(true);
  });
});
