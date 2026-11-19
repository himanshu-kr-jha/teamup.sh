import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useRemoteParticipants, useRoomContext } from '@livekit/components-react';
import { PhoneOutgoing, UserPlus } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { FormDialog } from '@/shared/components/FormDialog';
import { PeoplePicker, addLabel } from '@/shared/components/PeoplePicker';
import { callsApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { cn } from '@/shared/utils/cn';
import { outcomeMessage, withRinging, withoutRinging, type RingOutcome, type Ringing } from './addPeople';
import { personOf } from './devices';

export interface AddedRings {
  ringing: Ringing[];
  ring: (userIds: string[], nameOf: (id: string) => string) => Promise<void>;
  cancel: (person: Ringing) => Promise<void>;
}

/**
 * Who is being rung into this call, by anybody in it, and how each ring ends.
 * Everybody in the call hears that somebody declined or did not answer; the
 * call itself carries on whatever they say.
 */
export function useAddedRings(callId: string): AddedRings {
  const { on } = useWebSocket();
  const toast = useToast();
  const toastRef = useRef(toast.toast);
  toastRef.current = toast.toast;
  const remote = useRemoteParticipants();
  const [ringing, setRinging] = useState<Ringing[]>([]);

  useEffect(() => {
    const offAdding = on('call.adding', (msg) => {
      const p = msg.payload as { callId?: string; userId?: string; name?: string };
      if (p?.callId !== callId || !p.userId) return;
      setRinging((list) => withRinging(list, { userId: p.userId!, name: p.name || 'Somebody' }));
    });
    const offResult = on('call.ring.result', (msg) => {
      const p = msg.payload as { callId?: string; userId?: string; name?: string; outcome?: RingOutcome };
      if (p?.callId !== callId || !p.userId || !p.outcome) return;
      setRinging((list) => withoutRinging(list, [p.userId!]));
      const said = outcomeMessage(p.name || 'They', p.outcome);
      if (said) toastRef.current(said);
    });
    return () => {
      offAdding();
      offResult();
    };
  }, [on, callId]);

  // Whoever has arrived is no longer being rung, whichever word came first.
  useEffect(() => {
    setRinging((list) => withoutRinging(list, remote.map((p) => personOf(p.identity))));
  }, [remote]);

  const ring = useCallback(
    async (userIds: string[], nameOf: (id: string) => string) => {
      const res = await callsApi.ring(callId, userIds);
      // Busy and on leave are said to everybody by the server, as any ring's end is.
      for (const person of res.data.rung) {
        if (person.status === 'ringing') {
          setRinging((list) => withRinging(list, { userId: person.userId, name: person.name || nameOf(person.userId) }));
        }
      }
    },
    [callId]
  );

  const cancel = useCallback(
    async (person: Ringing) => {
      try {
        await callsApi.cancelRing(callId, person.userId);
        toastRef.current(`Stopped ringing ${person.name}.`);
      } catch (err) {
        // Most often it stopped by itself a moment ago; the strip follows the server.
        toast.error(apiErrorMessage(err, `Could not stop ringing ${person.name}.`));
      }
      setRinging((list) => withoutRinging(list, [person.userId]));
    },
    [callId, toast]
  );

  return { ringing, ring, cancel };
}

/** The button in the call's title bar that opens the dialog. */
export function AddPeopleButton({ rings }: { rings: AddedRings }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className="h-7 w-7"
        aria-label="Add people"
        title="Add people from your organization"
        onClick={() => setOpen(true)}
      >
        <UserPlus className="h-4 w-4" />
      </Button>
      <AddPeopleDialog open={open} onOpenChange={setOpen} rings={rings} />
    </>
  );
}

/** Choosing colleagues to ring into the call. Whoever is here, or already being rung, is not offered. */
function AddPeopleDialog({ open, onOpenChange, rings }: { open: boolean; onOpenChange: (open: boolean) => void; rings: AddedRings }) {
  const room = useRoomContext();
  const remote = useRemoteParticipants();
  const [chosen, setChosen] = useState<string[]>([]);
  const { data } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: open });

  const people = useMemo(() => {
    const here = new Set([personOf(room.localParticipant.identity), ...remote.map((p) => personOf(p.identity)), ...rings.ringing.map((p) => p.userId)]);
    return ((data?.data ?? []) as Array<{ id: string; name: string; email?: string; avatarUrl?: string | null; isActive?: boolean }>)
      .filter((u) => u.isActive !== false && !here.has(u.id))
      .map((u) => ({ id: u.id, name: u.name, email: u.email, avatarUrl: u.avatarUrl }));
  }, [data, room, remote, rings.ringing]);
  const nameOf = (id: string) => people.find((p) => p.id === id)?.name ?? 'Somebody';
  // Somebody who arrived, or started being rung, while the dialog was open is not rung again.
  const stillChosen = chosen.filter((id) => people.some((p) => p.id === id));

  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setChosen([]);
        onOpenChange(next);
      }}
      title="Add people"
      description="Ring colleagues from your organization into this call. They come straight in when they answer. To bring in somebody from outside, use Guests."
      submitLabel={addLabel(stillChosen.length, 'Ring', (n) => `Ring ${n} people`)}
      disabledReason={stillChosen.length === 0 ? 'Choose who to ring' : undefined}
      onSubmit={async () => {
        await rings.ring(stillChosen, nameOf);
        setChosen([]);
      }}
    >
      <PeoplePicker
        people={people}
        selected={stillChosen}
        onChange={setChosen}
        empty="Everybody in your organization is already in this call or being rung."
        listClassName="max-h-[min(16rem,40vh)]"
      />
    </FormDialog>
  );
}

/** "Ringing Ravi..." for each person being rung, with a way to stop. */
export function RingingStrip({ rings, compact }: { rings: AddedRings; compact: boolean }) {
  if (rings.ringing.length === 0) return null;
  return (
    <ul
      aria-label="Being rung into this call"
      className={cn(
        'flex shrink-0 flex-wrap items-center gap-1.5 border-b border-border bg-muted text-sm',
        compact ? 'px-2 py-1' : 'justify-center px-3 py-2'
      )}
    >
      {rings.ringing.map((person) => (
        <li
          key={person.userId}
          className="flex min-w-0 max-w-full items-center gap-1.5 rounded-full border border-border bg-background py-0.5 pl-2.5 pr-0.5"
        >
          <PhoneOutgoing className="h-3.5 w-3.5 shrink-0 animate-pulse text-primary" aria-hidden="true" />
          <span className="min-w-0 truncate">Ringing {person.name}{'…'}</span>
          <button
            type="button"
            onClick={() => void rings.cancel(person)}
            aria-label={`Stop ringing ${person.name}`}
            className="shrink-0 rounded-full px-2 py-0.5 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            Cancel
          </button>
        </li>
      ))}
    </ul>
  );
}
