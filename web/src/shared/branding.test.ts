import { describe, expect, it } from 'vitest';
import indexHtml from '../../index.html?raw';
import { BRANDING_STORAGE_KEY, hexToHslTokens, setTokens, textOn, tokensFor } from './branding';

describe('brand colours', () => {
  it("turns TeamUp's own indigo into the tokens globals.css already has", () => {
    expect(hexToHslTokens('#6366f1')).toBe('238.7 83.5% 66.7%');
  });

  it('puts light text on dark colours and dark text on light ones', () => {
    expect(textOn('#1e3a8a')).toBe('light');
    expect(textOn('#475569')).toBe('light');
    expect(textOn('#fde047')).toBe('dark');
    expect(textOn('#ffffff')).toBe('dark');
  });

  it('refuses what is not a colour', () => {
    expect(tokensFor('')).toBeNull();
    expect(tokensFor('red')).toBeNull();
    expect(tokensFor('#12345')).toBeNull();
  });
});

function fakeRoot() {
  const props = new Map<string, string>();
  return {
    props,
    style: {
      setProperty: (k: string, v: string) => props.set(k, v),
      removeProperty: (k: string) => props.delete(k),
    },
  };
}

describe('the startup script in index.html', () => {
  it('puts the remembered colour on the page exactly as the app does', () => {
    const tokens = tokensFor('#0d9488')!;
    const fromApp = fakeRoot();
    setTokens(tokens, fromApp as unknown as HTMLElement);

    const fromScript = fakeRoot();
    const doc = { documentElement: { ...fromScript, classList: { toggle: () => undefined } } };
    const storage = { getItem: (k: string) => (k === BRANDING_STORAGE_KEY ? JSON.stringify(tokens) : null) };
    const script = indexHtml.match(/<head>[\s\S]*?<script>([\s\S]*?)<\/script>/)![1];
    new Function('document', 'localStorage', 'matchMedia', script)(doc, storage, () => ({ matches: false }));

    expect(Object.fromEntries(fromScript.props)).toEqual(Object.fromEntries(fromApp.props));
  });

  it('leaves the default alone when nothing is remembered', () => {
    const root = fakeRoot();
    const doc = { documentElement: { ...root, classList: { toggle: () => undefined } } };
    const script = indexHtml.match(/<head>[\s\S]*?<script>([\s\S]*?)<\/script>/)![1];
    new Function('document', 'localStorage', 'matchMedia', script)(doc, { getItem: () => null }, () => ({ matches: false }));
    expect(root.props.size).toBe(0);
  });
});
