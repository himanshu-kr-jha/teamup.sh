import { useState } from 'react';
import { useRoomContext } from '@livekit/components-react';
import { MonitorUp, MonitorX } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';

/**
 * What the person presenting sees where their own shared screen would be (the
 * owner's report of 19 November 2026). Shown their own screen live, a person
 * sharing the whole screen, or the tab TeamUp is in, captured it showing itself,
 * over and over, an endless mirror; as in Google Meet, they see this instead,
 * with a button to stop. Everybody else sees the screen.
 *
 * `size` follows the tile: the stage has room to say more; the strip beside it
 * is covered by a button that puts the share on stage, so there is no button of
 * its own there; the corner window has room for the button and little else.
 */
export function PresentingCard({ size }: { size: 'stage' | 'corner' | 'strip' }) {
  const room = useRoomContext();
  const toast = useToast();
  const [stopping, setStopping] = useState(false);

  const stop = async () => {
    setStopping(true);
    try {
      await room.localParticipant.setScreenShareEnabled(false);
    } catch {
      toast.error('Could not stop sharing your screen.');
      setStopping(false);
    }
  };

  return (
    <div
      className={cn(
        'flex h-full w-full flex-col items-center justify-center bg-slate-900 text-center text-white',
        size === 'stage' ? 'gap-3 p-6' : size === 'corner' ? 'gap-1.5 p-2 pb-8' : 'gap-1 p-2'
      )}
      data-testid="presenting-card"
    >
      <span
        className={cn(
          'flex shrink-0 items-center justify-center rounded-full bg-white/10 text-white',
          size === 'stage' ? 'h-14 w-14' : 'h-8 w-8'
        )}
      >
        <MonitorUp className={size === 'stage' ? 'h-7 w-7' : 'h-4 w-4'} aria-hidden="true" />
      </span>
      <p className={cn('font-medium leading-tight', size === 'stage' ? 'text-base sm:text-lg' : 'text-xs')}>You are presenting</p>
      {size === 'stage' && (
        <p className="max-w-xs text-sm leading-snug text-white/70">
          Everybody else in the call sees your screen.
        </p>
      )}
      {size !== 'strip' && (
        <Button
          type="button"
          size="sm"
          variant="secondary"
          className={cn('shrink-0', size === 'corner' && 'h-7 px-2.5 text-xs')}
          disabled={stopping}
          onClick={() => void stop()}
        >
          <MonitorX className={cn('mr-1.5', size === 'stage' ? 'h-4 w-4' : 'h-3.5 w-3.5')} aria-hidden="true" />
          Stop presenting
        </Button>
      )}
    </div>
  );
}
