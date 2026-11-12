import type { ReactNode } from 'react';
import { cn } from '@/shared/utils/cn';

/**
 * A label as a coloured dot and its name. Its colour as the background made
 * the name unreadable on most colours, and differently so in the dark theme.
 */
export function LabelBadge({ name, color, className, children }: { name: string; color?: string; className?: string; children?: ReactNode }) {
  return (
    <span
      className={cn(
        'inline-flex max-w-full items-center gap-1.5 rounded-full border border-border bg-background px-2 py-0.5 text-xs font-medium text-foreground',
        className
      )}
      data-testid="task-label"
    >
      <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: color || '#6366f1' }} aria-hidden="true" />
      <span className="truncate">{name}</span>
      {children}
    </span>
  );
}
