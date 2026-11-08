import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { orgApi } from '@/shared/api/client';
import { UserAvatar } from '@/shared/components/UserAvatar';
import { useAuth } from '@/features/auth/AuthProvider';
import { cn } from '@/shared/utils/cn';
import { splitMentions, usernameOf, type Mentionable } from './mentions';
import { LinkedText } from '@/shared/components/LinkedText';

/** Everybody in the organization, as mentions need them. The same query the rest of the app reads. */
export function useMentionables(): Array<Mentionable & { avatarUrl?: string }> {
  const { data } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });
  return useMemo(
    () =>
      ((data?.data ?? []) as Array<{ id: string; name: string; email?: string; avatarUrl?: string; isActive?: boolean }>)
        .filter((u) => u.isActive !== false)
        .map((u) => ({ id: u.id, name: u.name, email: u.email, avatarUrl: u.avatarUrl })),
    [data]
  );
}

/**
 * A message's text, with web addresses as links and the people it names shown
 * as @Name. Naming you stands out more than naming anybody else. Addresses are
 * found first, so an @ inside one, as in medium.com/@name, stays in the link.
 */
export function MessageText({ content, isOwn }: { content: string; isOwn: boolean }) {
  const people = useMentionables();
  const { user } = useAuth();
  const withMentions = (text: string) =>
    splitMentions(text, people).map((part, i) =>
      'person' in part ? (
        <span
          key={i}
          data-testid="mention"
          title={`@${usernameOf(part.person)}`}
          className={cn(
            'rounded px-0.5 font-semibold',
            isOwn
              ? 'bg-primary-foreground/20'
              : part.person.id === user?.id
                ? 'bg-amber-300/50 text-foreground dark:bg-amber-500/30'
                : 'bg-primary/15 text-primary'
          )}
        >
          @{part.person.name}
        </span>
      ) : (
        <span key={i}>{part.text}</span>
      )
    );
  return (
    <p className="whitespace-pre-wrap break-anywhere">
      <LinkedText text={content} onPrimary={isOwn} renderText={withMentions} />
    </p>
  );
}

/** The people offered while an @-word is being typed, above the message box. */
export function MentionSuggestions({
  people,
  active,
  onPick,
  onHover,
}: {
  people: Array<Mentionable & { avatarUrl?: string }>;
  active: number;
  onPick: (person: Mentionable) => void;
  onHover: (index: number) => void;
}) {
  if (people.length === 0) return null;
  return (
    <ul
      role="listbox"
      aria-label="People to mention"
      className="absolute bottom-full left-0 z-20 mb-2 w-[min(18rem,calc(100vw-5rem))] overflow-hidden rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-md"
    >
      {people.map((person, i) => (
        <li key={person.id}>
          <button
            type="button"
            role="option"
            aria-selected={i === active}
            // Before the input loses focus, so the caret is still where the @-word is.
            onMouseDown={(e) => {
              e.preventDefault();
              onPick(person);
            }}
            onMouseEnter={() => onHover(i)}
            className={cn(
              'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm',
              i === active ? 'bg-accent text-accent-foreground' : ''
            )}
          >
            <UserAvatar name={person.name} src={person.avatarUrl} className="h-6 w-6" fallbackClassName="text-[10px]" />
            <span className="min-w-0 flex-1 truncate">{person.name}</span>
            <span className="shrink-0 text-xs text-muted-foreground">@{usernameOf(person)}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}
