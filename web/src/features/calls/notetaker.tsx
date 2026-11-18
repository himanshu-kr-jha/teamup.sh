import { useEffect, useState, useSyncExternalStore } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useParticipants, useRemoteParticipants, useRoomContext } from '@livekit/components-react';
import { RoomEvent, type RemoteParticipant } from 'livekit-client';
import { NotebookPen, PauseCircle } from 'lucide-react';
import { callsApi, guestApi, type CallNotesState, type NotesPause } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { Label } from '@/shared/components/ui/label';
import { Switch } from '@/shared/components/ui/switch';
import { cn } from '@/shared/utils/cn';

/** The note-taker's identity in a room starts with this (internal/notetaker). */
export const NOTETAKER_PREFIX = 'notes-';

/** The LiveKit data topic that tells a room its notes were turned on or off (internal/calls/notes.go). */
export const NOTES_TOPIC = 'teamup.meeting-notes';

export function isNotetaker(p: { identity: string; isAgent?: boolean }): boolean {
  return p.identity.startsWith(NOTETAKER_PREFIX) || !!p.isAgent;
}

/**
 * The other people in the call: everybody else connected, without the
 * note-taker, which is not a person. Counts, "Still there?" and whether you
 * are last out all go by this.
 */
export function useOtherPeople() {
  return useRemoteParticipants().filter((p) => !isNotetaker(p));
}

/** One line of the transcript as the page shows it: what was said, or where notes were not taken. */
export type TranscriptEntry =
  | { kind: 'said'; at: string; speaker: string; text: string }
  | { kind: 'gap'; at: string; text: string };

/**
 * The transcript in the order it was said, by when each stretch began, with a
 * line where notes were paused (or, off from the start, where they began).
 */
export function transcriptWithGaps(
  lines: Array<{ speaker: string; startedAt: string; text: string }>,
  pauses: NotesPause[] = [],
  time: (at: string) => string = clockTime
): TranscriptEntry[] {
  const entries: TranscriptEntry[] = lines.map((l) => ({ kind: 'said', at: l.startedAt, speaker: l.speaker, text: l.text }));
  for (const p of pauses) {
    const at = p.from ?? p.to;
    if (at) entries.push({ kind: 'gap', at, text: pauseText(p, time) });
  }
  // Stable, so lines that began together keep the order they were kept in.
  return entries
    .map((e, i) => ({ e, i, t: new Date(e.at).getTime() }))
    .sort((a, b) => a.t - b.t || a.i - b.i)
    .map(({ e }) => e);
}

/** A room's word that its notes changed, from the server only: nobody else can say so. */
export function notesFromPacket(
  payload: Uint8Array,
  from: { identity: string } | undefined,
  topic: string | undefined
): CallNotesState | null {
  if (topic !== NOTES_TOPIC || from) return null;
  try {
    const st = JSON.parse(new TextDecoder().decode(payload)) as Partial<CallNotesState>;
    if (typeof st.on !== 'boolean' || !Array.isArray(st.changes)) return null;
    return { callId: String(st.callId ?? ''), available: st.available !== false, on: st.on, changes: st.changes };
  } catch {
    return null;
  }
}

/** A time of day as the person reading it keeps time: 10:05. */
export function clockTime(at: string): string {
  return new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export type NotesBannerState = { tone: 'on' | 'paused'; text: string } | null;

/**
 * What the banner says. Notes being taken are said only while the note-taker
 * is in the call; paused notes are said with who paused them and when, so
 * everybody knows nothing is written down.
 */
export function notesBannerText(
  state: CallNotesState | undefined,
  present: boolean,
  asGuest: boolean,
  time: (at: string) => string = clockTime
): NotesBannerState {
  if (!state || state.on) {
    if (!present) return null;
    return {
      tone: 'on',
      text: asGuest
        ? 'Notes are being taken, including what you say, for the minutes of this meeting.'
        : 'Notes are being taken. Everybody here gets the minutes when the call ends.',
    };
  }
  const last = state.changes[state.changes.length - 1];
  if (!last || last.on) return null;
  return { tone: 'paused', text: `Notes paused by ${last.name} at ${time(last.at)}. Nothing said now is written down.` };
}

/** What the button does next: pause notes being taken, resume paused ones, or start ones off from the start. */
export function notesAction(state: CallNotesState): 'Pause notes' | 'Resume notes' | 'Start notes' {
  if (state.on) return 'Pause notes';
  return state.changes.length > 0 ? 'Resume notes' : 'Start notes';
}

/** A stretch when notes were off, said plainly, as the minutes say it. */
export function pauseText(p: NotesPause, time: (at: string) => string = clockTime): string {
  if (p.from && p.to) return `Notes were paused from ${time(p.from)} to ${time(p.to)} by ${p.by}.`;
  if (p.from) return `Notes were paused at ${time(p.from)} by ${p.by}, until the end of the call.`;
  if (p.to) return `Notes were off until ${time(p.to)}, when ${p.resumedBy} started them.`;
  return 'Notes were off for the whole call.';
}

/** Where a call's notes state comes from: a member's call, or a guest's link. */
export interface NotesSource {
  key: readonly unknown[];
  load: () => Promise<CallNotesState>;
}

export function memberNotes(callId: string): NotesSource {
  return { key: ['call-notes', callId], load: async () => (await callsApi.notes(callId)).data };
}

export function guestNotes(token: string, rejoinKey: string): NotesSource {
  return { key: ['guest-notes', token], load: async () => (await guestApi.notes(token, rejoinKey)).data };
}

// Whether the call on this page has its notes paused, for the note-taker's own
// tile, which has no source of its own. One call is open at a time.
// Counted, as the banner and the button each say so.
let pausedSays = 0;
const pausedListeners = new Set<() => void>();
function saysPaused(by: 1 | -1) {
  pausedSays += by;
  pausedListeners.forEach((l) => l());
}

/** Whether the open call's notes are paused, as its banner last heard. */
export function useNotesPaused(): boolean {
  return useSyncExternalStore(
    (l) => {
      pausedListeners.add(l);
      return () => pausedListeners.delete(l);
    },
    () => pausedSays > 0
  );
}

/** A call's notes state, kept up to date by the room's word whenever anybody changes it. */
export function useCallNotes(source: NotesSource | undefined): CallNotesState | undefined {
  const room = useRoomContext();
  const queryClient = useQueryClient();
  const key = source?.key ?? ['call-notes', 'none'];
  const { data } = useQuery({
    queryKey: key,
    queryFn: () => source!.load(),
    enabled: !!source,
    retry: false,
    staleTime: 60_000,
  });
  const keyText = JSON.stringify(key);
  useEffect(() => {
    if (!source) return;
    const queryKey = JSON.parse(keyText) as unknown[];
    const onData = (payload: Uint8Array, from?: RemoteParticipant, _kind?: unknown, topic?: string) => {
      const st = notesFromPacket(payload, from, topic);
      if (st) queryClient.setQueryData(queryKey, st);
    };
    // A word missed while the connection was down: ask again.
    const onReconnected = () => void queryClient.invalidateQueries({ queryKey });
    room.on(RoomEvent.DataReceived, onData);
    room.on(RoomEvent.Reconnected, onReconnected);
    return () => {
      room.off(RoomEvent.DataReceived, onData);
      room.off(RoomEvent.Reconnected, onReconnected);
    };
    // The key's text, not the array, so a new array each render does not listen again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [room, queryClient, keyText, !!source]);
  const paused = !!source && !!data && !data.on;
  useEffect(() => {
    if (!paused) return;
    saysPaused(1);
    return () => saysPaused(-1);
  }, [paused]);
  return data;
}

/**
 * Says, to everybody in the call, that notes are being taken and who gets the
 * minutes, or that they are paused and by whom. The note-taker is never
 * hidden: people should know what is written down.
 */
export function NotesBanner({ asGuest = false, source }: { asGuest?: boolean; source?: NotesSource }) {
  const participants = useParticipants();
  const state = useCallNotes(source);
  const said = notesBannerText(state, participants.some(isNotetaker), asGuest);
  if (!said) return null;
  const paused = said.tone === 'paused';
  const Icon = paused ? PauseCircle : NotebookPen;
  return (
    <div
      role="status"
      data-testid="notes-banner"
      data-state={said.tone}
      className={cn(
        'flex shrink-0 items-center justify-center gap-2 border-b px-3 py-1.5 text-center text-xs',
        paused ? 'border-amber-500/40 bg-amber-500/15' : 'border-border bg-primary/10'
      )}
    >
      <Icon className={cn('h-3.5 w-3.5 shrink-0', paused ? 'text-amber-600 dark:text-amber-400' : 'text-primary')} aria-hidden="true" />
      <span>{said.text}</span>
    </div>
  );
}

/**
 * Pause, resume or start the notes, beside the other call controls. Anybody in
 * the call may, so anybody can stop being written down; everybody sees who did.
 */
export function NotesButton({ callId, compact = false }: { callId: string; compact?: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const source = memberNotes(callId);
  const state = useCallNotes(source);
  const [busy, setBusy] = useState(false);
  if (!state?.available) return null;
  const action = notesAction(state);
  const button = (
    <button
      type="button"
      className={cn(
        compact
          ? 'inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-accent disabled:opacity-50 sm:hidden'
          : 'lk-button',
        !state.on && 'text-amber-600 dark:text-amber-400'
      )}
      aria-label={action}
      title={action}
      disabled={busy}
      data-testid="notes-button"
      onClick={async () => {
        setBusy(true);
        try {
          const res = await callsApi.setNotes(callId, !state.on);
          queryClient.setQueryData(source.key, res.data);
        } catch (err) {
          toast.error(apiErrorMessage(err, 'Could not change the notes.'));
        } finally {
          setBusy(false);
        }
      }}
    >
      <span className="relative inline-flex">
        <NotebookPen className={compact ? 'h-4 w-4' : 'h-5 w-5'} aria-hidden="true" />
        {/* Struck through while off, as a microphone that is off is. */}
        {!state.on && (
          <span
            className={cn(
              'absolute left-1/2 top-1/2 h-0.5 -translate-x-1/2 -translate-y-1/2 -rotate-45 rounded-full bg-current',
              compact ? 'w-5' : 'w-6'
            )}
            aria-hidden="true"
          />
        )}
      </span>
    </button>
  );
  // LiveKit's own style shows every lk-button, so the one under the call is
  // hidden on a phone by what holds it.
  return compact ? button : <span className="hidden sm:contents">{button}</span>;
}

/** The "Take notes" switch where a call or meeting is made. Hidden where there is no note-taker. */
export function TakeNotesSwitch({
  id,
  checked,
  onCheckedChange,
}: {
  id: string;
  checked: boolean;
  onCheckedChange: (on: boolean) => void;
}) {
  return (
    <div className="flex items-start gap-3">
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} className="mt-0.5" />
      <Label htmlFor={id} className="cursor-pointer text-sm font-normal leading-snug">
        <span className="font-medium">Take notes</span>
        <span className="block text-muted-foreground">
          The note-taker writes down what is said, for the minutes. Anybody in the call can pause it.
        </span>
      </Label>
    </div>
  );
}

/** Whether the organization has a note-taker, and whether a new call takes notes unless told otherwise. */
export function useNotesDefault(enabled: boolean) {
  const { data } = useQuery({
    queryKey: ['notes-default'],
    queryFn: async () => (await callsApi.notesDefault()).data,
    enabled,
    staleTime: 60_000,
  });
  return data;
}
