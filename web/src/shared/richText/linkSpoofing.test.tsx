// @vitest-environment happy-dom
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { RichText } from './RichText';
import { parseBlocks, toHtml } from './format';

// A named link whose name is itself an address could send somebody to one site
// while saying it is another. Wherever such a link is drawn, the real site shows.

function shown(text: string) {
  const html = renderToStaticMarkup(
    <MemoryRouter>
      <RichText text={text} />
    </MemoryRouter>
  );
  const el = document.createElement('div');
  el.innerHTML = html;
  return { text: el.textContent ?? '', links: [...el.querySelectorAll('a')].map((a) => a.getAttribute('href')) };
}

describe('a named link that passes for another address', () => {
  it('shows where it really goes when its name is an address on another site', () => {
    const { text, links } = shown('Log in at [https://bank.example/login](https://evil.example/steal)');
    expect(text).toContain('https://bank.example/login (evil.example)');
    expect(links).toEqual(['https://evil.example/steal']);
  });

  it('does so for a bare domain as the name too, however it is formatted', () => {
    expect(shown('[paypal.com](https://evil.example)').text).toBe('paypal.com (evil.example)');
    expect(shown('[*www.paypal.com*](https://evil.example)').text).toBe('www.paypal.com (evil.example)');
  });

  it('shows the punycode of a look-alike address', () => {
    expect(shown('[apple.com](https://аpple.com)').text).toBe('apple.com (xn--pple-43d.com)');
  });

  it('adds nothing when the name is words or the same site', () => {
    expect(shown('[the notes](https://docs.example/n)').text).toBe('the notes');
    expect(shown('[docs.example](https://docs.example/notes)').text).toBe('docs.example');
    expect(shown('[www.docs.example](https://docs.example/notes)').text).toBe('www.docs.example');
  });

  it('never puts a second link, to the address in the name, inside the first', () => {
    expect(shown('[https://bank.example](https://evil.example)').links).toEqual(['https://evil.example']);
  });

  it('carries the real site into HTML copied out of TeamUp as well', () => {
    expect(toHtml(parseBlocks('[paypal.com](https://evil.example/x)'))).toBe(
      '<p><a href="https://evil.example/x">paypal.com</a> (evil.example)</p>'
    );
  });
});
