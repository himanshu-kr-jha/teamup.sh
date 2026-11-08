import { useParticipants } from '@livekit/components-react';
import { NotebookPen } from 'lucide-react';

/** The note-taker's identity in a room starts with this (internal/notetaker). */
export const NOTETAKER_PREFIX = 'notes-';

export function isNotetaker(p: { identity: string; isAgent?: boolean }): boolean {
  return p.identity.startsWith(NOTETAKER_PREFIX) || !!p.isAgent;
}

/**
 * Says, to everybody in the call, that notes are being taken and who gets the
 * minutes. The note-taker is never hidden: people should know what is written down.
 */
export function NotesBanner({ asGuest = false }: { asGuest?: boolean }) {
  const participants = useParticipants();
  if (!participants.some(isNotetaker)) return null;
  return (
    <div
      role="status"
      data-testid="notes-banner"
      className="flex shrink-0 items-center justify-center gap-2 border-b border-border bg-primary/10 px-3 py-1.5 text-xs"
    >
      <NotebookPen className="h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />
      <span>
        {asGuest
          ? 'Notes are being taken, including what you say, for the minutes of this meeting.'
          : 'Notes are being taken. Everybody here gets the minutes when the call ends.'}
      </span>
    </div>
  );
}
