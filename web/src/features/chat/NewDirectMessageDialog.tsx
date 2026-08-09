import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { Input } from '@/shared/components/ui/input';
import { channelsApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';

/**
 * Finding somebody and talking to them one to one. There is exactly one
 * conversation per pair, so picking the same person again reopens the existing
 * history rather than starting a second empty thread.
 */
export function NewDirectMessageDialog({
  open,
  onOpenChange,
  onOpened,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpened: (channelId: string) => void;
}) {
  const toast = useToast();
  const { user } = useAuth();
  const [query, setQuery] = useState('');
  const [opening, setOpening] = useState('');

  const { data, isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: () => orgApi.listUsers(),
    enabled: open,
  });

  const term = query.trim().toLowerCase();
  const people = (data?.data ?? [])
    .filter((u: any) => u.id !== user?.id && u.isActive !== false)
    .filter((u: any) =>
      !term ||
      (u.name ?? '').toLowerCase().includes(term) ||
      (u.email ?? '').toLowerCase().includes(term)
    );

  const start = async (userId: string) => {
    setOpening(userId);
    try {
      const { data: channel } = await channelsApi.openDirect(userId);
      onOpenChange(false);
      setQuery('');
      onOpened(channel.id);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not open that conversation.'));
    } finally {
      setOpening('');
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setQuery('');
        onOpenChange(o);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New chat</DialogTitle>
          <DialogDescription>Pick somebody to talk to one to one.</DialogDescription>
        </DialogHeader>

        <div className="relative">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input
            autoFocus
            placeholder="Search by name or email"
            aria-label="Search people"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="pl-10"
          />
        </div>

        {isLoading && <p className="text-sm text-muted-foreground">Loading people…</p>}
        {!isLoading && people.length === 0 && (
          <p className="text-sm text-muted-foreground">Nobody matches that.</p>
        )}

        <ul className="max-h-72 space-y-1 overflow-y-auto">
          {people.map((person: any) => (
            <li key={person.id}>
              <button
                onClick={() => start(person.id)}
                disabled={!!opening}
                className="flex w-full items-center gap-3 rounded-md p-2 text-left hover:bg-accent disabled:opacity-60"
              >
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10">
                  <span className="text-sm font-medium text-primary">
                    {(person.name || '?').charAt(0).toUpperCase()}
                  </span>
                </div>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{person.name}</p>
                  <p className="truncate text-xs text-muted-foreground">{person.email}</p>
                </div>
                {opening === person.id && (
                  <span className="text-xs text-muted-foreground">Opening…</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
