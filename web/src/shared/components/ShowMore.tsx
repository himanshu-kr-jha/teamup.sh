import { useState } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { showMoreText, visiblePart } from '@/shared/utils/showMore';

/** A long list kept short until somebody asks for the rest. */
export function useShowMore<T>(items: T[], first: number, keep: 'start' | 'end' = 'start') {
  const [expanded, setExpanded] = useState(false);
  const { visible, hidden } = visiblePart(items, first, expanded, keep);
  return { visible, hidden, expanded, toggle: () => setExpanded((e) => !e), needed: items.length > first };
}

export function ShowMoreButton({
  state,
  noun,
  earlier,
}: {
  state: { hidden: number; expanded: boolean; toggle: () => void; needed: boolean };
  noun: [string, string];
  /** The hidden ones came before, as with comments, whose newest are shown. */
  earlier?: boolean;
}) {
  if (!state.needed) return null;
  const Icon = state.expanded !== !!earlier ? ChevronUp : ChevronDown;
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="h-8 w-full text-xs text-muted-foreground hover:text-foreground"
      aria-expanded={state.expanded}
      onClick={state.toggle}
    >
      <Icon className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
      {showMoreText(state.hidden, state.expanded, noun, earlier)}
    </Button>
  );
}
