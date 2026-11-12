import { splitLinks } from '@/shared/utils/links';

/**
 * Formatted messages.
 *
 * A message is stored as the text it was written as, with the marks people
 * already type in WhatsApp, Slack and Telegram:
 *
 *   *bold*  _italic_  ~struck~  `code`  ```a block of code```
 *   - a list   1. a numbered list   > a quote
 *
 * **bold** and ~~struck~~, as Markdown writes them, are read too, and so is a
 * [named link](https://…). Because the marks are the ones other chat apps
 * use, the text itself is what goes out to them, and formatted text pasted in
 * from a document or a web page is turned into the same marks.
 */

export type Inline =
  | { t: 'text'; text: string }
  | { t: 'code'; text: string }
  | { t: 'link'; href: string; children: Inline[] }
  | { t: 'bold' | 'italic' | 'strike'; children: Inline[] };

export type ListItem = { level: number; marker: string; content: Inline[] };

export type Block =
  | { t: 'para'; lines: Inline[][] }
  | { t: 'list'; ordered: boolean; items: ListItem[] }
  | { t: 'quote'; blocks: Block[] }
  | { t: 'code'; text: string };

// ---------------------------------------------------------------------------
// Reading the marks

const FENCE = /^\s*```/;
const BULLET = /^(\s*)[-*•]\s+(.*)$/;
const NUMBERED = /^(\s*)(\d{1,4})[.)]\s+(.*)$/;
const QUOTE = /^\s*>\s?(.*)$/;

/** A message's text read into blocks: paragraphs, lists, quotes and code. */
export function parseBlocks(text: string): Block[] {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const blocks: Block[] = [];
  let para: Inline[][] = [];
  const endPara = () => {
    if (para.length) blocks.push({ t: 'para', lines: para });
    para = [];
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    // ``` opens a block of code, unless the closing ``` is on the same line,
    // which is code in a sentence.
    if (FENCE.test(line) && !line.replace(FENCE, '').includes('```')) {
      endPara();
      const first = line.replace(FENCE, '');
      const body: string[] = [];
      // Text right after the opening fence is code, not a language name: chat
      // apps put it there.
      if (first.trim() && !/^[A-Za-z0-9+#-]+$/.test(first.trim())) body.push(first);
      let j = i + 1;
      let closed = false;
      for (; j < lines.length; j++) {
        const end = lines[j].indexOf('```');
        if (end >= 0) {
          if (lines[j].slice(0, end).length) body.push(lines[j].slice(0, end));
          closed = true;
          break;
        }
        body.push(lines[j]);
      }
      if (!closed && body.length === 0) {
        // A lone ``` with nothing after it is just three backticks.
        para.push(parseInline(line));
        continue;
      }
      blocks.push({ t: 'code', text: body.join('\n') });
      i = closed ? j : lines.length;
      continue;
    }

    if (QUOTE.test(line)) {
      endPara();
      const inner: string[] = [];
      let j = i;
      for (; j < lines.length && QUOTE.test(lines[j]); j++) inner.push(lines[j].replace(QUOTE, '$1'));
      blocks.push({ t: 'quote', blocks: parseBlocks(inner.join('\n')) });
      i = j - 1;
      continue;
    }

    if (BULLET.test(line) || NUMBERED.test(line)) {
      endPara();
      const ordered = NUMBERED.test(line);
      const items: ListItem[] = [];
      let j = i;
      for (; j < lines.length; j++) {
        const b = BULLET.exec(lines[j]);
        const n = b ? null : NUMBERED.exec(lines[j]);
        if (!b && !n) break;
        if (!!n !== ordered && items.length) break;
        const indent = (b ? b[1] : n![1]).replace(/\t/g, '  ').length;
        items.push({
          level: Math.min(3, Math.floor(indent / 2)),
          marker: n ? `${n[2]}.` : '•',
          content: parseInline(b ? b[2] : n![3]),
        });
      }
      blocks.push({ t: 'list', ordered, items });
      i = j - 1;
      continue;
    }

    if (!line.trim()) {
      endPara();
      continue;
    }
    para.push(parseInline(line));
  }
  endPara();
  return blocks;
}

type Atom = { atom: Inline };
type Token = string | Atom;

const MD_LINK = /\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g;
const CODE_SPAN = /```([^`\n]+)```|`([^`\n]+)`/g;

/** One line of text read into its runs of formatting. */
export function parseInline(line: string): Inline[] {
  // Code and addresses are read first and kept whole: a * or _ inside them is
  // part of them, not formatting.
  const tokens: Token[] = [];
  const pushText = (s: string) => {
    for (const part of splitLinks(s)) {
      if (part.href) tokens.push({ atom: { t: 'link', href: part.href, children: [{ t: 'text', text: part.text }] } });
      else for (const ch of part.text) tokens.push(ch);
    }
  };
  const special = [...line.matchAll(MD_LINK), ...line.matchAll(CODE_SPAN)].sort((a, b) => a.index! - b.index!);
  let at = 0;
  for (const m of special) {
    if (m.index! < at) continue;
    pushText(line.slice(at, m.index));
    if (m[0].startsWith('[')) {
      tokens.push({ atom: { t: 'link', href: m[2], children: parseInline(m[1]) } });
    } else {
      tokens.push({ atom: { t: 'code', text: m[1] ?? m[2] } });
    }
    at = m.index! + m[0].length;
  }
  pushText(line.slice(at));
  return emphasis(tokens, 0, tokens.length);
}

const MARKS: Array<{ mark: string; kind: 'bold' | 'italic' | 'strike' }> = [
  { mark: '**', kind: 'bold' },
  { mark: '__', kind: 'bold' },
  { mark: '~~', kind: 'strike' },
  { mark: '*', kind: 'bold' },
  { mark: '_', kind: 'italic' },
  { mark: '~', kind: 'strike' },
];

const isWord = (t: Token | undefined) => typeof t === 'string' && /[\p{L}\p{N}]/u.test(t);
const isSpace = (t: Token | undefined) => t === undefined || (typeof t === 'string' && /\s/.test(t));

function markAt(tokens: Token[], i: number, mark: string): boolean {
  for (let k = 0; k < mark.length; k++) if (tokens[i + k] !== mark[k]) return false;
  return true;
}

function emphasis(tokens: Token[], from: number, to: number): Inline[] {
  const out: Inline[] = [];
  let text = '';
  const flush = () => {
    if (text) out.push({ t: 'text', text });
    text = '';
  };

  for (let i = from; i < to; ) {
    const tok = tokens[i];
    if (typeof tok !== 'string') {
      flush();
      out.push(tok.atom);
      i++;
      continue;
    }
    let matched = false;
    for (const { mark, kind } of MARKS) {
      if (!markAt(tokens, i, mark)) continue;
      const open = i + mark.length;
      // Opens only before text and not in the middle of a word, so snake_case
      // and 2*3*4 stay as they are.
      if (isSpace(tokens[open]) || isWord(tokens[i - 1]) || tokens[open] === mark[0]) break;
      for (let j = open + 1; j + mark.length <= to; j++) {
        if (!markAt(tokens, j, mark)) continue;
        if (isSpace(tokens[j - 1]) || isWord(tokens[j + mark.length]) || tokens[j + mark.length] === mark[0]) continue;
        flush();
        out.push({ t: kind, children: emphasis(tokens, open, j) });
        i = j + mark.length;
        matched = true;
        break;
      }
      break;
    }
    if (!matched) {
      text += tok;
      i++;
    }
  }
  flush();
  return out;
}

// ---------------------------------------------------------------------------
// Writing it out again

/** The words alone, as a notification or a preview shows them. */
export function plainText(text: string): string {
  return parseBlocks(text)
    .map((b) => blockPlain(b))
    .join('\n');
}

function inlinePlain(runs: Inline[]): string {
  return runs
    .map((r) => {
      if (r.t === 'text' || r.t === 'code') return r.text;
      if (r.t === 'link') {
        const label = inlinePlain(r.children);
        return sameAddress(label, r.href) ? label : `${label} (${r.href})`;
      }
      return inlinePlain(r.children);
    })
    .join('');
}

function blockPlain(b: Block): string {
  switch (b.t) {
    case 'para':
      return b.lines.map(inlinePlain).join('\n');
    case 'code':
      return b.text;
    case 'quote':
      return b.blocks.map(blockPlain).join('\n');
    case 'list':
      return b.items.map((it) => `${'  '.repeat(it.level)}${it.marker === '•' ? '-' : it.marker} ${inlinePlain(it.content)}`).join('\n');
  }
}

function sameAddress(label: string, href: string): boolean {
  const bare = (s: string) => s.replace(/^https?:\/\//i, '').replace(/\/$/, '');
  return bare(label) === bare(href);
}

/**
 * The text with the marks chat apps read: WhatsApp, Telegram, Slack and
 * TeamUp itself. A named link becomes its name and its address, since only
 * TeamUp reads [name](address).
 */
export function chatText(blocks: Block[], { namedLinks = false }: { namedLinks?: boolean } = {}): string {
  const inline = (runs: Inline[]): string =>
    runs
      .map((r) => {
        switch (r.t) {
          case 'text':
            return r.text;
          case 'code':
            return `\`${r.text}\``;
          case 'bold':
            return wrap('*', inline(r.children));
          case 'italic':
            return wrap('_', inline(r.children));
          case 'strike':
            return wrap('~', inline(r.children));
          case 'link': {
            const label = inline(r.children);
            if (sameAddress(label, r.href)) return r.href;
            return namedLinks ? `[${label}](${r.href})` : `${label} (${r.href})`;
          }
        }
      })
      .join('');
  const block = (b: Block): string => {
    switch (b.t) {
      case 'para':
        return b.lines.map(inline).join('\n');
      case 'code':
        return '```\n' + b.text + '\n```';
      case 'quote':
        return b.blocks
          .map(block)
          .join('\n\n')
          .split('\n')
          .map((l) => `> ${l}`.trimEnd())
          .join('\n');
      case 'list':
        return b.items.map((it) => `${'  '.repeat(it.level)}${it.marker === '•' ? '-' : it.marker} ${inline(it.content)}`).join('\n');
    }
  };
  return blocks.map(block).join('\n\n');
}

/** Marks around a run, with its spaces kept outside them, which is where they must be to count. */
function wrap(mark: string, inner: string): string {
  const m = /^(\s*)([\s\S]*?)(\s*)$/.exec(inner)!;
  return m[2] ? `${m[1]}${mark}${m[2]}${mark}${m[3]}` : inner;
}

const escapeHtml = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

/** The text as HTML, for pasting into a document, an email or a page that keeps formatting. */
export function toHtml(blocks: Block[]): string {
  const inline = (runs: Inline[]): string =>
    runs
      .map((r) => {
        switch (r.t) {
          case 'text':
            return escapeHtml(r.text);
          case 'code':
            return `<code>${escapeHtml(r.text)}</code>`;
          case 'bold':
            return `<strong>${inline(r.children)}</strong>`;
          case 'italic':
            return `<em>${inline(r.children)}</em>`;
          case 'strike':
            return `<s>${inline(r.children)}</s>`;
          case 'link':
            return `<a href="${escapeHtml(r.href)}">${inline(r.children)}</a>`;
        }
      })
      .join('');
  const block = (b: Block): string => {
    switch (b.t) {
      case 'para':
        return `<p>${b.lines.map(inline).join('<br>')}</p>`;
      case 'code':
        return `<pre><code>${escapeHtml(b.text)}</code></pre>`;
      case 'quote':
        return `<blockquote>${b.blocks.map(block).join('')}</blockquote>`;
      case 'list':
        return listHtml(b.items, b.ordered, inline);
    }
  };
  return blocks.map(block).join('');
}

function listHtml(items: ListItem[], ordered: boolean, inline: (r: Inline[]) => string): string {
  const tag = ordered ? 'ol' : 'ul';
  let html = '';
  let depth = -1;
  for (const it of items) {
    if (it.level > depth) {
      // A deeper list opens inside the item before it.
      for (; depth < it.level; depth++) html += `<${tag}>`;
    } else {
      html += '</li>';
      for (; depth > it.level; depth--) html += `</${tag}></li>`;
    }
    html += `<li>${inline(it.content)}`;
  }
  html += '</li>';
  for (; depth > 0; depth--) html += `</${tag}></li>`;
  return html + `</${tag}>`;
}

// ---------------------------------------------------------------------------
// Formatted text pasted in

const BLOCK_TAGS = new Set([
  'P', 'DIV', 'SECTION', 'ARTICLE', 'HEADER', 'FOOTER', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6',
  'UL', 'OL', 'LI', 'BLOCKQUOTE', 'PRE', 'TABLE', 'TR', 'HR', 'DL', 'DT', 'DD', 'FIGURE',
]);
const SKIP_TAGS = new Set(['SCRIPT', 'STYLE', 'HEAD', 'TITLE', 'META', 'TEMPLATE', 'NOSCRIPT', 'IMG', 'SVG', 'BUTTON']);

type Style = { bold: boolean; italic: boolean; strike: boolean; code: boolean };

/**
 * HTML from the clipboard, as copied from Google Docs, Word, Gmail, Notion, a
 * web page or TeamUp itself, written out with the marks a message uses, so it
 * looks the same once sent. Null when there is no text in it.
 */
export function htmlToChatText(html: string): string | null {
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const blocks = readBlocks(doc.body);
  const text = chatText(blocks, { namedLinks: true }).replace(/\n{3,}/g, '\n\n').trim();
  return text ? text : null;
}

function styleOf(el: Element, inherited: Style): Style {
  const s = { ...inherited };
  const tag = el.tagName;
  if (tag === 'B' || tag === 'STRONG' || /^H[1-6]$/.test(tag) || tag === 'TH') s.bold = true;
  if (tag === 'I' || tag === 'EM' || tag === 'CITE') s.italic = true;
  if (tag === 'S' || tag === 'DEL' || tag === 'STRIKE') s.strike = true;
  if (tag === 'CODE' || tag === 'KBD' || tag === 'SAMP' || tag === 'TT') s.code = true;
  const css = (el as HTMLElement).style;
  if (css) {
    const weight = css.fontWeight;
    // Google Docs wraps everything in <b style="font-weight:normal">.
    if (weight === 'normal' || weight === '400' || weight === 'lighter') s.bold = false;
    else if (weight === 'bold' || weight === 'bolder' || Number(weight) >= 600) s.bold = true;
    if (css.fontStyle === 'italic') s.italic = true;
    else if (css.fontStyle === 'normal') s.italic = false;
    if ((css.textDecoration || css.textDecorationLine || '').includes('line-through')) s.strike = true;
  }
  return s;
}

function readBlocks(root: Element): Block[] {
  const blocks: Block[] = [];
  let lines: Inline[][] = [];
  let line: Inline[] = [];
  const endLine = () => {
    if (line.some((r) => r.t !== 'text' || r.text.trim())) lines.push(trimRuns(line));
    line = [];
  };
  const endPara = () => {
    endLine();
    if (lines.length) blocks.push({ t: 'para', lines });
    lines = [];
  };

  const plain: Style = { bold: false, italic: false, strike: false, code: false };

  const walk = (node: Node, style: Style) => {
    if (node.nodeType === 3) {
      const text = (node.textContent ?? '').replace(/[\s\u00a0]+/g, ' ');
      if (text) line.push(...styled(text, style));
      return;
    }
    if (node.nodeType !== 1) return;
    const el = node as Element;
    const tag = el.tagName;
    if (SKIP_TAGS.has(tag)) return;
    if (tag === 'BR') {
      endLine();
      return;
    }
    if (tag === 'PRE') {
      endPara();
      blocks.push({ t: 'code', text: (el.textContent ?? '').replace(/\n$/, '') });
      return;
    }
    if (tag === 'UL' || tag === 'OL') {
      endPara();
      const items: ListItem[] = [];
      readList(el, 0, items, style);
      if (items.length) blocks.push({ t: 'list', ordered: tag === 'OL', items });
      return;
    }
    if (tag === 'BLOCKQUOTE') {
      endPara();
      const inner = readBlocks(el);
      if (inner.length) blocks.push({ t: 'quote', blocks: inner });
      return;
    }
    if (tag === 'A') {
      line.push(...inlineOf(el, style));
      return;
    }
    const s = styleOf(el, style);
    const block = BLOCK_TAGS.has(tag);
    if (block) endPara();
    if (tag === 'TD' || tag === 'TH') {
      if (line.length) line.push({ t: 'text', text: ' | ' });
    }
    for (const child of Array.from(el.childNodes)) walk(child, s);
    if (block) endPara();
  };

  const readList = (list: Element, level: number, items: ListItem[], style: Style) => {
    let n = Number(list.getAttribute('start') ?? '1') || 1;
    for (const li of Array.from(list.children)) {
      if (li.tagName !== 'LI') continue;
      const content: Inline[] = [];
      const nested: Element[] = [];
      for (const child of Array.from(li.childNodes)) {
        if (child.nodeType === 1 && ((child as Element).tagName === 'UL' || (child as Element).tagName === 'OL')) nested.push(child as Element);
        else content.push(...inlineOf(child, styleOf(li, style)));
      }
      items.push({ level, marker: list.tagName === 'OL' ? `${n++}.` : '•', content: trimRuns(content) });
      for (const sub of nested) readList(sub, Math.min(3, level + 1), items, style);
    }
  };

  for (const child of Array.from(root.childNodes)) walk(child, plain);
  endPara();
  return blocks;
}

/** Everything inside an element as one line of runs. */
function inlineOf(node: Node, style: Style): Inline[] {
  if (node.nodeType === 3) return styled((node.textContent ?? '').replace(/[\s\u00a0]+/g, ' '), style);
  if (node.nodeType !== 1) return [];
  const el = node as Element;
  if (SKIP_TAGS.has(el.tagName)) return [];
  if (el.tagName === 'BR') return [{ t: 'text', text: ' ' }];
  if (el.tagName === 'A') {
    const href = el.getAttribute('href') ?? '';
    const label = trimRuns(Array.from(el.childNodes).flatMap((c) => inlineOf(c, styleOf(el, style))));
    return /^https?:\/\//i.test(href) && label.length ? [{ t: 'link', href, children: label }] : label;
  }
  const s = styleOf(el, style);
  const runs = Array.from(el.childNodes).flatMap((c) => inlineOf(c, s));
  return BLOCK_TAGS.has(el.tagName) ? [...runs, { t: 'text', text: ' ' }] : runs;
}

function styled(text: string, s: Style): Inline[] {
  if (s.code) return text.trim() ? [{ t: 'code', text }] : [{ t: 'text', text }];
  let run: Inline = { t: 'text', text };
  if (!text.trim()) return [run];
  if (s.strike) run = { t: 'strike', children: [run] };
  if (s.italic) run = { t: 'italic', children: [run] };
  if (s.bold) run = { t: 'bold', children: [run] };
  return [run];
}

/** A line's runs with the spaces at its ends dropped, and runs of the same formatting joined. */
function trimRuns(runs: Inline[]): Inline[] {
  const merged: Inline[] = [];
  for (const r of runs) {
    const prev = merged[merged.length - 1];
    if (prev && prev.t === 'text' && r.t === 'text') prev.text = (prev.text + r.text).replace(/ {2,}/g, ' ');
    else if (prev && r.t === prev.t && (r.t === 'bold' || r.t === 'italic' || r.t === 'strike') && 'children' in prev)
      prev.children = trimRuns([...prev.children, ...r.children]);
    else merged.push(r.t === 'text' ? { ...r } : r);
  }
  const first = merged[0];
  if (first?.t === 'text') first.text = first.text.trimStart();
  const last = merged[merged.length - 1];
  if (last?.t === 'text') last.text = last.text.trimEnd();
  return merged.filter((r) => r.t !== 'text' || r.text);
}

// ---------------------------------------------------------------------------
// Writing with the keyboard or the buttons

export type Edit = { text: string; start: number; end: number };

/** Marks put around what is selected, or taken off if they are already there. */
export function toggleMark(text: string, start: number, end: number, mark: string): Edit {
  const before = text.slice(0, start);
  const sel = text.slice(start, end);
  const after = text.slice(end);
  if (before.endsWith(mark) && after.startsWith(mark)) {
    return { text: before.slice(0, -mark.length) + sel + after.slice(mark.length), start: start - mark.length, end: end - mark.length };
  }
  if (sel.length > 2 * mark.length && sel.startsWith(mark) && sel.endsWith(mark)) {
    const inner = sel.slice(mark.length, -mark.length);
    return { text: before + inner + after, start, end: start + inner.length };
  }
  // Spaces selected at the ends stay outside the marks.
  const lead = /^\s*/.exec(sel)![0];
  const trail = /\s*$/.exec(sel.slice(lead.length))![0];
  const core = sel.slice(lead.length, sel.length - trail.length);
  const close = mark === '```' ? '```' : mark;
  const open = mark === '```' && core.includes('\n') ? '```\n' : mark;
  const shut = mark === '```' && core.includes('\n') ? '\n```' : close;
  const next = before + lead + open + core + shut + trail + after;
  const s = start + lead.length + open.length;
  return { text: next, start: s, end: s + core.length };
}

/** Every line the selection touches started with a prefix, or has it taken off if all of them have it. */
export function toggleLinePrefix(text: string, start: number, end: number, kind: 'bullet' | 'numbered' | 'quote'): Edit {
  const lineStart = text.lastIndexOf('\n', start - 1) + 1;
  let lineEnd = text.indexOf('\n', end > start && text[end - 1] === '\n' ? end - 1 : end);
  if (lineEnd < 0) lineEnd = text.length;
  const lines = text.slice(lineStart, lineEnd).split('\n');
  const has = { bullet: /^\s*[-*•]\s/, numbered: /^\s*\d{1,4}[.)]\s/, quote: /^\s*>\s?/ }[kind];
  const all = lines.every((l) => has.test(l));
  const changed = lines.map((l, i) =>
    all ? l.replace(has, '') : kind === 'bullet' ? `- ${l}` : kind === 'numbered' ? `${i + 1}. ${l}` : `> ${l}`
  );
  const block = changed.join('\n');
  const next = text.slice(0, lineStart) + block + text.slice(lineEnd);
  return { text: next, start: lineStart + block.length, end: lineStart + block.length };
}
