import { Palmtree, PartyPopper } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { markLabel, type DayMark } from './dayMarks';

/** Leave or a holiday, on the day it covers. It has a date and no time, so it sits with the day. */
export function DayMarkChip({ mark, whose, compact }: { mark: DayMark; whose?: string; compact?: boolean }) {
  const label = markLabel(mark, whose);
  const Icon = mark.kind === 'holiday' ? PartyPopper : Palmtree;
  return (
    <span
      className={cn(
        'flex w-full min-w-0 items-center gap-1 rounded border px-1 py-0.5 text-left text-xs',
        mark.kind === 'holiday'
          ? 'border-pink-500/40 bg-pink-50 text-pink-800 dark:bg-pink-950/40 dark:text-pink-200'
          : 'border-slate-400/50 bg-slate-100 text-slate-700 dark:border-slate-500/50 dark:bg-slate-800/60 dark:text-slate-200',
        // A phone's week has no room for words: the icon, and the words for a screen reader.
        compact && 'justify-center sm:justify-start'
      )}
      data-testid="day-mark"
      title={label}
    >
      <Icon className="h-3 w-3 shrink-0" aria-hidden="true" />
      {compact ? (
        <>
          <span className="sr-only">{label}</span>
          <span className="hidden min-w-0 truncate sm:inline" aria-hidden="true">
            {label}
          </span>
        </>
      ) : (
        <span className="min-w-0 truncate">{label}</span>
      )}
    </span>
  );
}
