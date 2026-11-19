import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { channelsApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { usePeopleDirectory } from '@/shared/components/search/directory';
import { NoMatch, SearchInput, useListKeys } from '@/shared/components/search/listKeys';
import { byName, indexBy, personFacts, search } from '@/shared/utils/peopleSearch';
import { cn } from '@/shared/utils/cn';

interface Person {
  id: string;
  name: string;
  email?: string;
  jobTitle?: string;
  avatarUrl?: string;
  isActive?: boolean;
}

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

  const directory = usePeopleDirectory();
  const index = useMemo(
    () =>
      indexBy(
        ((data?.data ?? []) as Person[]).filter((u) => u.id !== user?.id && u.isActive !== false).sort(byName),
        (u) => personFacts({ ...u, teams: directory.about(u.id)?.teams })
      ),
    [data, user?.id, directory]
  );
  const people = useMemo(() => search(index, query), [index, query]);

  const keys = useListKeys({ shown: people, query, setQuery, onPick: (p: Person) => !opening && start(p.id) });

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

        <SearchInput
          placeholder="Search by name, email, role or team"
          aria-label="Search people"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={keys.onKeyDown}
          {...keys.inputProps}
        />

        {isLoading && <p className="text-sm text-muted-foreground">Loading people…</p>}
        {!isLoading && people.length === 0 && (query.trim() ? <NoMatch query={query} /> : <p className="text-sm text-muted-foreground">Nobody else is here yet.</p>)}

        <ul
          id={keys.listId}
          ref={keys.listRef}
          onScroll={keys.onScroll}
          role="listbox"
          aria-label="People"
          className="max-h-72 space-y-1 overflow-y-auto overscroll-contain"
        >
          {people.slice(0, keys.limit).map((person, i) => (
            <li
              key={person.id}
              id={keys.optionId(i)}
              role="option"
              aria-selected={keys.active === i}
              onClick={() => !opening && start(person.id)}
              onMouseMove={() => keys.active !== i && keys.setActive(i)}
              aria-disabled={!!opening || undefined}
              className={cn(
                'flex w-full cursor-pointer items-center gap-3 rounded-md p-2 text-left hover:bg-accent aria-disabled:cursor-default aria-disabled:opacity-60',
                keys.active === i && 'bg-accent'
              )}
            >
              <PersonAvatar userId={person.id} name={person.name} src={person.avatarUrl} fallbackClassName="bg-primary/10 font-medium text-primary" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{person.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  {[person.jobTitle, person.email].filter(Boolean).join(' · ')}
                </p>
              </div>
              {opening === person.id && <span className="text-xs text-muted-foreground">Opening…</span>}
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
