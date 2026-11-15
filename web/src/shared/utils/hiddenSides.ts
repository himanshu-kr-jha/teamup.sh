/**
 * Which ends of a sideways-scrolling strip have something out of sight. A pixel of
 * slack, because a zoomed page reports fractions and the end is never quite reached.
 */
export function hiddenSides(scrollLeft: number, clientWidth: number, scrollWidth: number) {
  return { left: scrollLeft > 1, right: scrollLeft + clientWidth < scrollWidth - 1 };
}
