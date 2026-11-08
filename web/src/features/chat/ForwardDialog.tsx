import { createContext, useContext, useMemo, useState } from 'react';
import { PERMS, can } from '@/shared/permissions';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Forward, Hash, Loader2, Megaphone, Paperclip, Search, User } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { channelsApi, messagesApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';
import { cn } from '@/shared/utils/cn';

/** As much of a message as forwarding needs. */
export interface ForwardableMessage {
  id: string;
  content: string;
  attachments?: Array<{ filename: string }>;
}

/** Lets any message on the page ask to be forwarded, without threading a prop through. */
export const ForwardContext = createContext<((message: ForwardableMessage) => void) | null>(null);

export function useForward() {
  return useContext(ForwardContext);
}

/** The most a single forward reaches; the server says the same. */
export const MAX_FORWARD_TARGETS = 10;

/**
 * Passing a message on to other conversations: pick one or several, and it
 * arrives there marked Forwarded, with its files.
 */
export function ForwardDialog({
  message,
  onClose,
}: {
  message: ForwardableMessage | null;
  onClose: () => void;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [query, setQuery] = useState('');
  const [chosen, setChosen] = useState<string[]>([]);
  const [sending, setSending] = useState(false);
  const isAdmin = can(user, PERMS.channelsAll);

  const { data } = useQuery({ queryKey: ['channels'], queryFn: () => channelsApi.list(), enabled: !!message });
  const term = query.trim().toLowerCase();
  const conversations = useMemo(
    () =>
      ((data?.data ?? []) as Array<{ id: string; name?: string; type: string }>)
        // Only administrators post announcements, so the others are not offered them.
        .filter((c) => c.type !== 'announcement' || isAdmin)
        .filter((c) => !term || (c.name ?? '').toLowerCase().includes(term)),
    [data, term, isAdmin]
  );

  const close = () => {
    setChosen([]);
    setQuery('');
    onClose();
  };

  const toggle = (id: string) =>
    setChosen((now) =>
      now.includes(id) ? now.filter((x) => x !== id) : now.length >= MAX_FORWARD_TARGETS ? now : [...now, id]
    );

  const send = async () => {
    if (!message || chosen.length === 0) return;
    setSending(true);
    try {
      await messagesApi.forward(message.id, chosen);
      toast.success(chosen.length === 1 ? 'Forwarded.' : `Forwarded to ${chosen.length} conversations.`);
      void queryClient.invalidateQueries({ queryKey: ['channels'] });
      for (const id of chosen) void queryClient.invalidateQueries({ queryKey: ['messages', id] });
      close();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not forward that message.'));
    } finally {
      setSending(false);
    }
  };

  const files = message?.attachments?.length ?? 0;

  return (
    <Dialog open={!!message} onOpenChange={(open) => !open && close()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Forward message</DialogTitle>
          <DialogDescription>It arrives marked Forwarded, from you, with its files.</DialogDescription>
        </DialogHeader>

        {message && (
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm" data-testid="forward-preview">
            {message.content && <p className="line-clamp-3 whitespace-pre-wrap break-anywhere">{message.content}</p>}
            {files > 0 && (
              <p className={cn('flex items-center gap-1 text-xs text-muted-foreground', message.content && 'mt-1')}>
                <Paperclip className="h-3 w-3" aria-hidden="true" />
                {files === 1 ? message.attachments![0].filename : `${files} files`}
              </p>
            )}
          </div>
        )}

        <div className="relative">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Find a conversation"
            className="pl-8"
            aria-label="Find a conversation"
          />
        </div>

        <ul className="-mx-1 max-h-72 overflow-y-auto" role="listbox" aria-multiselectable="true" aria-label="Conversations">
          {conversations.length === 0 && (
            <li className="px-3 py-6 text-center text-sm text-muted-foreground">No conversations match.</li>
          )}
          {conversations.map((c) => {
            const picked = chosen.includes(c.id);
            const full = !picked && chosen.length >= MAX_FORWARD_TARGETS;
            return (
              <li key={c.id}>
                <button
                  type="button"
                  role="option"
                  aria-selected={picked}
                  disabled={full}
                  onClick={() => toggle(c.id)}
                  className={cn(
                    'flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors',
                    picked ? 'bg-primary/10' : 'hover:bg-accent',
                    full && 'opacity-50'
                  )}
                >
                  {c.type === 'dm' ? (
                    <User className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  ) : c.type === 'announcement' ? (
                    <Megaphone className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  ) : (
                    <Hash className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  )}
                  <span className="min-w-0 flex-1 truncate">{c.name || 'Unnamed'}</span>
                  <span
                    className={cn(
                      'flex h-5 w-5 shrink-0 items-center justify-center rounded border',
                      picked ? 'border-primary bg-primary text-primary-foreground' : 'border-input'
                    )}
                    aria-hidden="true"
                  >
                    {picked && <Check className="h-3.5 w-3.5" />}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>

        <DialogFooter className="gap-2 sm:items-center">
          <span className="mr-auto text-xs text-muted-foreground">
            {chosen.length === 0
              ? 'Pick one or more'
              : chosen.length >= MAX_FORWARD_TARGETS
                ? `${MAX_FORWARD_TARGETS} at most`
                : `${chosen.length} chosen`}
          </span>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button onClick={send} disabled={chosen.length === 0 || sending}>
            {sending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Forward className="mr-2 h-4 w-4" />}
            Forward
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
