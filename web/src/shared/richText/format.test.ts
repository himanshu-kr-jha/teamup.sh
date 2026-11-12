// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { chatText, htmlToChatText, parseBlocks, parseInline, plainText, toHtml, toggleLinePrefix, toggleMark } from './format';

const html = (text: string) => toHtml(parseBlocks(text));

describe('reading the marks', () => {
  it('reads bold, italic, struck and code, as chat apps write them and as Markdown does', () => {
    expect(html('*bold* _italic_ ~struck~ `code`')).toBe(
      '<p><strong>bold</strong> <em>italic</em> <s>struck</s> <code>code</code></p>'
    );
    expect(html('**bold** and ~~struck~~')).toBe('<p><strong>bold</strong> and <s>struck</s></p>');
    expect(html('*bold with _italic_ inside*')).toBe('<p><strong>bold with <em>italic</em> inside</strong></p>');
  });

  it('leaves marks that are not formatting alone', () => {
    expect(html('snake_case_name and 2*3*4')).toBe('<p>snake_case_name and 2*3*4</p>');
    expect(html('a * b * c')).toBe('<p>a * b * c</p>');
    expect(html('*not closed')).toBe('<p>*not closed</p>');
    expect(html('@first_last_name')).toBe('<p>@first_last_name</p>');
  });

  it('keeps addresses and code whole, whatever is in them', () => {
    const runs = parseInline('see https://example.com/a_b_c/*x* and `a*b*c`');
    expect(runs.map((r) => r.t)).toEqual(['text', 'link', 'text', 'code']);
    expect(html('*see https://x.com/a_b*')).toBe('<p><strong>see <a href="https://x.com/a_b">https://x.com/a_b</a></strong></p>');
  });

  it('reads a named link, and only to a web address', () => {
    expect(html('[the plan](https://example.com/plan)')).toBe('<p><a href="https://example.com/plan">the plan</a></p>');
    expect(html('[x](javascript:alert(1))')).toBe('<p>[x](javascript:alert(1))</p>');
  });

  it('keeps line breaks, and reads lists, quotes and code blocks', () => {
    expect(html('one\ntwo\n\nthree')).toBe('<p>one<br>two</p><p>three</p>');
    expect(html('- a\n- b\n  - c\n- d')).toBe('<ul><li>a</li><li>b<ul><li>c</li></ul></li><li>d</li></ul>');
    expect(html('1. first\n2. second')).toBe('<ol><li>first</li><li>second</li></ol>');
    expect(html('> quoted *bold*\n> more\nafter')).toBe(
      '<blockquote><p>quoted <strong>bold</strong><br>more</p></blockquote><p>after</p>'
    );
    expect(html('```\nconst a = *b*;\n```')).toBe('<pre><code>const a = *b*;</code></pre>');
    expect(html('```go\nx := 1\n```')).toBe('<pre><code>x := 1</code></pre>');
    expect(html('inline ```code``` here')).toBe('<p>inline <code>code</code> here</p>');
  });

  it('escapes what would otherwise be HTML', () => {
    expect(html('<b>hi</b> & "you"')).toBe('<p>&lt;b&gt;hi&lt;/b&gt; &amp; &quot;you&quot;</p>');
  });
});

describe('writing it out', () => {
  it('gives the words alone for a notification', () => {
    expect(plainText('*Hello* _there_, see [the plan](https://x.com/p)\n- one')).toBe(
      'Hello there, see the plan (https://x.com/p)\n- one'
    );
  });

  it('gives other chat apps the marks they read', () => {
    expect(chatText(parseBlocks('**bold** ~~gone~~ [plan](https://x.com/p) https://y.com'))).toBe(
      '*bold* ~gone~ plan (https://x.com/p) https://y.com'
    );
  });
});

describe('formatted text pasted in', () => {
  it('keeps bold, italic, links and line breaks from a web page or an email', () => {
    expect(
      htmlToChatText('<p>Hello <b>team</b>, <i>please</i> read <a href="https://x.com/doc">the doc</a>.</p><p>Thanks<br>Alice</p>')
    ).toBe('Hello *team*, _please_ read [the doc](https://x.com/doc).\n\nThanks\nAlice');
  });

  it('reads Google Docs, which marks formatting with styles inside a bold wrapper', () => {
    const docs =
      '<meta charset="utf-8"><b style="font-weight:normal;" id="docs-internal-guid-1"><p dir="ltr"><span style="font-weight:700;">Agenda</span></p>' +
      '<ul><li><p><span>Budget </span><span style="font-style:italic;">review</span></p></li><li><p><span>Hiring</span></p></li></ul></b>';
    expect(htmlToChatText(docs)).toBe('*Agenda*\n\n- Budget _review_\n- Hiring');
  });

  it('keeps numbered and nested lists, quotes and code', () => {
    expect(htmlToChatText('<ol><li>one<ul><li>inner</li></ul></li><li>two</li></ol>')).toBe('1. one\n  - inner\n2. two');
    expect(htmlToChatText('<blockquote>wise words</blockquote><pre>a  =  1\nb = 2</pre>')).toBe(
      '> wise words\n\n```\na  =  1\nb = 2\n```'
    );
  });

  it('keeps spaces outside the marks, where they must be to count', () => {
    expect(htmlToChatText('<b>bold </b>then')).toBe('*bold* then');
  });

  it('drops what is not text', () => {
    expect(htmlToChatText('<style>p{}</style><script>void 0</script><img src="a.png">')).toBeNull();
  });

  it('reads back what TeamUp itself copied', () => {
    const text = '*Minutes*\n\n- _one_\n- ~two~\n\n> said';
    expect(htmlToChatText(html(text))).toBe(text);
  });
});

describe('formatting with the keyboard or the buttons', () => {
  it('puts marks around the selection, and takes them off again', () => {
    const on = toggleMark('make this bold', 5, 9, '*');
    expect(on).toEqual({ text: 'make *this* bold', start: 6, end: 10 });
    expect(toggleMark(on.text, on.start, on.end, '*')).toEqual({ text: 'make this bold', start: 5, end: 9 });
  });

  it('leaves selected spaces outside the marks', () => {
    expect(toggleMark('a word here', 1, 7, '_').text).toBe('a _word_ here');
  });

  it('starts every selected line as a list item, or stops', () => {
    const on = toggleLinePrefix('one\ntwo', 0, 7, 'bullet');
    expect(on.text).toBe('- one\n- two');
    expect(toggleLinePrefix(on.text, 0, on.text.length, 'bullet').text).toBe('one\ntwo');
    expect(toggleLinePrefix('a\nb', 0, 3, 'numbered').text).toBe('1. a\n2. b');
  });
});
