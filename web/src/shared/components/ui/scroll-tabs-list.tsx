import { useEffect, useState, type ReactNode } from 'react';
import { TabsList } from './tabs';
import { cn } from '@/shared/utils/cn';
import { hiddenSides } from '@/shared/utils/hiddenSides';

/** Which ends of the strip are hiding tabs, kept up to date as it scrolls or resizes. */
function useHiddenSides(strip: HTMLElement | null) {
  const [sides, setSides] = useState({ left: false, right: false });
  useEffect(() => {
    if (!strip) return;
    const update = () => {
      const next = hiddenSides(strip.scrollLeft, strip.clientWidth, strip.scrollWidth);
      setSides((now) => (now.left === next.left && now.right === next.right ? now : next));
    };
    update();
    strip.addEventListener('scroll', update, { passive: true });
    // The strip for its own width, the tabs for theirs once the web font arrives.
    const observer = new ResizeObserver(update);
    [strip, ...Array.from(strip.children)].forEach((el) => observer.observe(el));
    const mutations = new MutationObserver(update);
    mutations.observe(strip, { childList: true });
    return () => {
      strip.removeEventListener('scroll', update);
      observer.disconnect();
      mutations.disconnect();
    };
  }, [strip]);
  return sides;
}

/**
 * A row of tabs that scrolls sideways when it does not fit, as it does on a
 * phone, and says so.
 *
 * With the scrollbar hidden, a strip of nine tabs cut off at the screen's edge
 * looked like a strip of four, and a deep link to the seventh opened with it out
 * of sight. Here the chosen tab is always brought into view, centred so its
 * neighbours show too, and a fade at either end says there is more that way.
 */
export function ScrollTabsList({
  value,
  className,
  wrapperClassName,
  fadeClassName = 'from-muted',
  children,
  ...rest
}: {
  /** The chosen tab, so a change of it scrolls it into view. */
  value: string;
  className?: string;
  wrapperClassName?: string;
  /** The colour the ends fade into: the strip's own background. */
  fadeClassName?: string;
  children: ReactNode;
  'aria-label'?: string;
}) {
  const [strip, setStrip] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    const active = strip?.querySelector<HTMLElement>('[data-state="active"]');
    if (!strip || !active) return;
    // Scrolled by hand rather than with scrollIntoView, which also scrolls the
    // page vertically to bring the strip itself into view.
    const left = active.offsetLeft - (strip.clientWidth - active.offsetWidth) / 2;
    strip.scrollTo({ left: Math.max(0, left) });
  }, [value, strip]);
  const hidden = useHiddenSides(strip);
  return (
    <div className={cn('relative flex min-w-0 max-w-full', wrapperClassName)}>
      <TabsList
        ref={setStrip}
        className={cn('w-full justify-start overflow-x-auto scrollbar-hide md:w-auto', className)}
        {...rest}
      >
        {children}
      </TabsList>
      {hidden.left && (
        <div
          aria-hidden
          data-testid="tabs-more-left"
          className={cn('pointer-events-none absolute inset-y-0 left-0 w-10 rounded-l-md bg-gradient-to-r to-transparent', fadeClassName)}
        />
      )}
      {hidden.right && (
        <div
          aria-hidden
          data-testid="tabs-more-right"
          className={cn('pointer-events-none absolute inset-y-0 right-0 w-10 rounded-r-md bg-gradient-to-l to-transparent', fadeClassName)}
        />
      )}
    </div>
  );
}
