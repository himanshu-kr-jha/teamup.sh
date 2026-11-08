import { describe, expect, it } from 'vitest';
import { inAppPath, splitLinks } from './links';

const links = (s: string) => splitLinks(s).filter((p) => p.href).map((p) => p.href);

describe('links in messages', () => {
  it('finds http, https and www. addresses among the words', () => {
    expect(splitLinks('see https://example.com/a?b=1#c now')).toEqual([
      { text: 'see ' },
      { text: 'https://example.com/a?b=1#c', href: 'https://example.com/a?b=1#c' },
      { text: ' now' },
    ]);
    expect(links('docs at www.example.org/guide')).toEqual(['https://www.example.org/guide']);
    expect(links('HTTP://EXAMPLE.COM')).toEqual(['HTTP://EXAMPLE.COM']);
    expect(links('two: http://a.example and https://b.example')).toEqual(['http://a.example', 'https://b.example']);
  });

  it('leaves the end of the sentence out of the address', () => {
    expect(links('Read https://example.com.')).toEqual(['https://example.com']);
    expect(links('(see https://example.com/x)')).toEqual(['https://example.com/x']);
    expect(links('https://en.wikipedia.org/wiki/Go_(game), it says')).toEqual(['https://en.wikipedia.org/wiki/Go_(game)']);
    expect(links('"https://example.com/q?x=1"!')).toEqual(['https://example.com/q?x=1']);
  });

  it('keeps an @ inside an address as part of it', () => {
    expect(links('https://medium.com/@ada/post')).toEqual(['https://medium.com/@ada/post']);
  });

  it('never links anything that is not a web address', () => {
    expect(links('javascript:alert(1) data:text/html,x notes.md ada@example.com https:// www.')).toEqual([]);
    expect(splitLinks('no links here')).toEqual([{ text: 'no links here' }]);
    expect(splitLinks('')).toEqual([]);
  });

  it('opens a link to TeamUp itself in place', () => {
    expect(inAppPath('https://teamup.example/calls/abc?x=1#y', 'https://teamup.example')).toBe('/calls/abc?x=1#y');
    expect(inAppPath('https://elsewhere.example/calls/abc', 'https://teamup.example')).toBeNull();
  });
});
