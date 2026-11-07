import { useCallback, useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Check, DoorOpen, Loader2, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useChime } from '@/shared/hooks/useChime';
import { cn } from '@/shared/utils/cn';
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';
import { useToast } from '@/shared/hooks/useToast';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useActiveCall } from './ActiveCallProvider';
import { addPrompt, removePrompt, removePromptsForCall, type KnockPrompt } from './knock';

/**
 * Somebody who followed a link to a private call and is waiting at the door.
 *
 * Mounted above the router and drawn over everything, the call window included,
 * because a member has to be able to answer wherever they are. With nobody in the
 * call the frame reaches the organizer and invited people, who may not be in it
 * either (decision 32).
 */
export function KnockCenter() {
  const { on, state } = useWebSocket();
  const { activeCall, watchedDoor } = useActiveCall();
  const { pathname } = useLocation();
  const { knock: chime } = useChime();
  const { notify } = useDesktopNotifications();
  const [prompts, setPrompts] = useState<KnockPrompt[]>([]);
  // Sounds once per request, however many ways it reaches this tab.
  const heard = useRef(new Set<string>());

  const announce = useCallback(
    (prompt: KnockPrompt) => {
      setPrompts((current) => addPrompt(current, prompt));
      if (heard.current.has(prompt.requestId)) return;
      heard.current.add(prompt.requestId);
      chime();
      if (document.visibilityState !== 'visible') {
        notify({ title: `${prompt.userName} asks to join`, body: 'Open TeamUp to let them in.', tag: `knock-${prompt.requestId}` });
      }
    },
    [chime, notify]
  );

  useEffect(() => {
    const stops = [
      on('call.knock', (msg) => {
        const p = msg.payload as KnockPrompt;
        if (p?.requestId && p.callId) announce(p);
      }),
      on('call.knock.resolved', (msg) => {
        const p = msg.payload as { requestId?: string };
        if (p?.requestId) setPrompts((current) => removePrompt(current, p.requestId!));
      }),
      on('call.ended', (msg) => {
        const p = msg.payload as { callId?: string; reason?: string };
        // Somebody declining does not end the call for the rest.
        if (!p?.callId || p.reason === 'declined' || p.reason === 'no_answer') return;
        setPrompts((current) => removePromptsForCall(current, p.callId!));
      }),
    ];
    return () => stops.forEach((stop) => stop());
  }, [on, announce]);

  // A knock that came before you did, or while the socket was down, is still
  // waiting. Whoever is in the call is who it is waiting on, and so is a member who
  // came from the notification to a meeting that has not started.
  const inCall = activeCall?.standing ? undefined : activeCall?.callId;
  const callId = inCall ?? watchedDoor ?? undefined;
  useEffect(() => {
    if (!callId || state !== 'ready') return;
    let stale = false;
    callsApi
      .requests(callId)
      .then((res) => {
        if (stale) return;
        res.data
          .filter((r) => r.status === 'pending')
          .forEach((r) =>
            announce({ callId, requestId: r.id, userId: r.userId, userName: r.userName, connected: !!inCall })
          );
      })
      .catch(() => {
        // Nothing to answer that can be seen right now. A new knock still arrives.
      });
    return () => {
      stale = true;
    };
  }, [callId, inCall, state, announce]);

  const dismiss = useCallback((requestId: string) => {
    setPrompts((current) => removePrompt(current, requestId));
  }, []);

  if (prompts.length === 0) return null;

  // Below the call's title bar when it fills the screen: leaving has to stay in reach.
  const overCall = !!activeCall && pathname === `/calls/${activeCall.callId}`;

  return (
    // pointer-events-auto because an open dialog turns pointer events off on the
    // whole page, and somebody at the door must not have to wait for it to close.
    <div
      className={cn(
        'pointer-events-auto fixed left-1/2 z-[60] w-[min(26rem,calc(100vw-1rem))] -translate-x-1/2 space-y-2',
        overCall ? 'top-12' : 'top-2'
      )}
      aria-live="assertive"
    >
      {prompts.slice(0, 3).map((prompt) => (
        <PromptCard key={prompt.requestId} prompt={prompt} onDone={() => dismiss(prompt.requestId)} />
      ))}
      {prompts.length > 3 && (
        <p className="rounded-md border border-border bg-card px-3 py-1.5 text-center text-xs text-muted-foreground shadow">
          {prompts.length - 3} more waiting
        </p>
      )}
    </div>
  );
}

function PromptCard({ prompt, onDone }: { prompt: KnockPrompt; onDone: () => void }) {
  const toast = useToast();
  const [answering, setAnswering] = useState<'admit' | 'deny' | null>(null);

  const { data } = useQuery({
    queryKey: ['call', prompt.callId],
    queryFn: () => callsApi.get(prompt.callId).then((res) => res.data),
    staleTime: 60_000,
    retry: false,
  });
  const title: string | undefined = data?.title?.trim() || undefined;

  const answer = async (how: 'admit' | 'deny') => {
    setAnswering(how);
    try {
      await (how === 'admit' ? callsApi.admit : callsApi.deny)(prompt.callId, prompt.requestId);
      onDone();
    } catch (err) {
      const status = (err as { response?: { status?: number } })?.response?.status;
      if (status === 409 || status === 403 || status === 404) {
        // Answered already, or over; either way there is nothing left to do here.
        toast.toast(`${prompt.userName} has already been answered.`);
        onDone();
      } else {
        toast.error(apiErrorMessage(err, 'Could not answer. Try again.'));
        setAnswering(null);
      }
    }
  };

  return (
    <div role="group" aria-label={`${prompt.userName} asks to join`} className="rounded-lg border border-border bg-card p-3 text-card-foreground shadow-2xl">
      <div className="flex items-start gap-3">
        <DoorOpen className="mt-0.5 h-5 w-5 shrink-0 text-primary" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <p className="break-anywhere text-sm font-medium">
            {prompt.userName} asks to join{title ? ` ${title}` : ' this call'}
          </p>
          {!prompt.connected && (
            <p className="text-xs text-muted-foreground">Nobody is in the call right now. You can answer from here.</p>
          )}
        </div>
      </div>
      <div className="mt-2 flex justify-end gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={!!answering}
          aria-label={`Deny ${prompt.userName}`}
          onClick={() => answer('deny')}
        >
          {answering === 'deny' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : <X className="mr-1 h-4 w-4" aria-hidden="true" />}
          Deny
        </Button>
        <Button size="sm" disabled={!!answering} aria-label={`Admit ${prompt.userName}`} onClick={() => answer('admit')}>
          {answering === 'admit' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : <Check className="mr-1 h-4 w-4" aria-hidden="true" />}
          Admit
        </Button>
      </div>
    </div>
  );
}
