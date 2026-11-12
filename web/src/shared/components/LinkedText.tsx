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
  return (
    <>
      {pieces.map((piece, i) =>
        piece.href ? (
          <TextLink key={i} href={piece.href} onPrimary={onPrimary}>
            {piece.text}
          </TextLink>
        ) : (
          <Fragment key={i}>{renderText(piece.text)}</Fragment>
        )
      )}
    </>
  );
}

/** A link in some text: another site in a new tab, TeamUp's own pages in place. */
export function TextLink({ href, onPrimary = false, children }: { href: string; onPrimary?: boolean; children: ReactNode }) {
  const className = cn(
    'underline underline-offset-2',
    onPrimary
      ? 'decoration-primary-foreground/60 hover:decoration-primary-foreground'
      : 'text-primary decoration-primary/40 hover:decoration-primary'
  );
  // Whatever the text sits in, such as a message with its own menu, must not
  // also act on a click on its link.
  const stop = (e: React.MouseEvent) => e.stopPropagation();
  const path = inAppPath(href);
  return path ? (
    <Link to={path} className={className} onClick={stop}>
      {children}
    </Link>
  ) : (
    <a href={href} target="_blank" rel="noopener noreferrer nofollow" className={className} onClick={stop}>
      {children}
    </a>
  );
}
