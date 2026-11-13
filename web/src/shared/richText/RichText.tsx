import { Fragment, useMemo, type ReactNode } from 'react';
import { cn } from '@/shared/utils/cn';
import { TextLink } from '@/shared/components/LinkedText';
import { hiddenHost, parseBlocks, runsText, type Block, type Inline } from './format';

/**
 * A message's text shown with its formatting: bold, italic, struck, code,
 * lists, quotes and links. renderText draws the plain runs, for example with
 * the people it mentions.
 */
export function RichText({
  text,
  onPrimary = false,
  renderText = (t) => t,
  className,
}: {
  text: string;
  /** On a primary-coloured background, such as your own chat message. */
  onPrimary?: boolean;
  renderText?: (text: string) => ReactNode;
  className?: string;
}) {
  const blocks = useMemo(() => parseBlocks(text), [text]);
  const tint = onPrimary ? 'bg-primary-foreground/15' : 'bg-foreground/[0.07]';
  const rule = onPrimary ? 'border-primary-foreground/50' : 'border-foreground/25';

  // Inside a link the words are the link's alone: an @ in an address, as in
  // medium.com/@name, is not somebody mentioned.
  const inline = (runs: Inline[], inLink = false): ReactNode =>
    runs.map((r, i) => {
      switch (r.t) {
        case 'text':
          return <Fragment key={i}>{inLink ? r.text : renderText(r.text)}</Fragment>;
        case 'code':
          return (
            <code key={i} className={cn('rounded px-1 py-px font-mono text-[0.88em]', tint)}>
              {r.text}
            </code>
          );
        case 'bold':
          return <strong key={i} className="font-semibold">{inline(r.children, inLink)}</strong>;
        case 'italic':
          return <em key={i}>{inline(r.children, inLink)}</em>;
        case 'strike':
          return <s key={i}>{inline(r.children, inLink)}</s>;
        case 'link': {
          const real = hiddenHost(runsText(r.children), r.href);
          return (
            <Fragment key={i}>
              <TextLink href={r.href} onPrimary={onPrimary}>
                {inline(r.children, true)}
              </TextLink>
              {real && <span className="opacity-75"> ({real})</span>}
            </Fragment>
          );
        }
      }
    });

  const block = (b: Block, i: number): ReactNode => {
    switch (b.t) {
      case 'para':
        return (
          <p key={i}>
            {b.lines.map((line, j) => (
              <Fragment key={j}>
                {j > 0 && <br />}
                {inline(line)}
              </Fragment>
            ))}
          </p>
        );
      case 'code':
        return (
          <pre key={i} className={cn('overflow-x-auto whitespace-pre rounded-md px-2.5 py-1.5 font-mono text-[0.85em] leading-snug', tint)}>
            <code>{b.text}</code>
          </pre>
        );
      case 'quote':
        return (
          <blockquote key={i} className={cn('space-y-1 border-l-2 pl-2.5 opacity-90', rule)}>
            {b.blocks.map(block)}
          </blockquote>
        );
      case 'list': {
        const List = b.ordered ? 'ol' : 'ul';
        return (
          <List key={i} className={cn('space-y-0.5 pl-5', b.ordered ? 'list-decimal' : 'list-disc')}>
            {b.items.map((it, j) => (
              <li
                key={j}
                value={b.ordered ? parseInt(it.marker, 10) || undefined : undefined}
                className={cn('pl-0.5', !b.ordered && it.level % 2 === 1 && 'list-[circle]')}
                style={it.level ? { marginLeft: `${it.level * 1.1}rem` } : undefined}
              >
                {inline(it.content)}
              </li>
            ))}
          </List>
        );
      }
    }
  };

  return <div className={cn('space-y-1.5 whitespace-pre-wrap break-anywhere', className)}>{blocks.map(block)}</div>;
}
