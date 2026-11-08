import { Fragment, useMemo, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { cn } from '@/shared/utils/cn';
import { inAppPath, splitLinks } from '@/shared/utils/links';

/**
 * Text with its web addresses as links: other sites in a new tab, TeamUp's own
 * pages in place. renderText draws the rest, for example with mentions.
 */
export function LinkedText({
  text,
  onPrimary = false,
  renderText = (t) => t,
}: {
  text: string;
  /** On a primary-coloured background, such as your own chat message. */
  onPrimary?: boolean;
  renderText?: (text: string) => ReactNode;
}) {
  const pieces = useMemo(() => splitLinks(text), [text]);
  const className = cn(
    'underline underline-offset-2',
    onPrimary
      ? 'decoration-primary-foreground/60 hover:decoration-primary-foreground'
      : 'text-primary decoration-primary/40 hover:decoration-primary'
  );
  // Whatever the text sits in, such as a message with its own menu, must not
  // also act on a click on its link.
  const stop = (e: React.MouseEvent) => e.stopPropagation();
  return (
    <>
      {pieces.map((piece, i) => {
        if (!piece.href) return <Fragment key={i}>{renderText(piece.text)}</Fragment>;
        const path = inAppPath(piece.href);
        return path ? (
          <Link key={i} to={path} className={className} onClick={stop}>
            {piece.text}
          </Link>
        ) : (
          <a key={i} href={piece.href} target="_blank" rel="noopener noreferrer nofollow" className={className} onClick={stop}>
            {piece.text}
          </a>
        );
      })}
    </>
  );
}
