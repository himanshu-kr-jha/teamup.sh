import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  CalendarDays,
  CheckSquare,
  FolderGit2,
  Hash,
  Keyboard,
  Megaphone,
  MessageSquare,
  NotebookPen,
  Plane,
  Search,
  Settings,
  Users,
  Video,
  type LucideIcon,
} from 'lucide-react';
import * as DialogPrimitive from '@radix-ui/react-dialog';
import { DialogOverlay, DialogPortal } from '@/shared/components/ui/dialog';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { channelsApi, orgApi, tasksApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useAuth } from '@/features/auth/AuthProvider';
import { useToast } from '@/shared/hooks/useToast';
import { useShortcutPanels } from '@/shared/shortcuts/ShortcutsProvider';
import { cn } from '@/shared/utils/cn';
import { Kbd } from './CheatSheet';
import { usePlaces } from './places';
import { rank } from './rank';

interface Item {
  key: string;
  group: 'Pages' | 'Unread' | 'People' | 'Channels' | 'Tasks';
  label: string;
  detail?: string;
  icon: ReactNode;
  run: () => void | Promise<void>;
}

const icon = (Icon: LucideIcon) => <Icon className="h-4 w-4 text-muted-foreground" aria-hidden="true" />;

const PER_GROUP = 6;

/**
 * Ctrl+K: one box to get to a person, a conversation, a task or a page,
 * without reaching for the mouse. It reads the same lists the app already
 * keeps (people, conversations, tasks) and filters them here, so it answers as
 * fast as you type.
 */
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          className={cn(
            'fixed left-1/2 top-[10vh] z-50 flex max-h-[min(32rem,80vh)] w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 flex-col overflow-hidden',
            'rounded-lg border border-border bg-popover text-popover-foreground shadow-2xl',
            'data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0'
          )}
          data-testid="command-palette"
          aria-describedby={undefined}
        >
          <DialogPrimitive.Title className="sr-only">Search and jump</DialogPrimitive.Title>
          {open && <Palette onDone={() => onOpenChange(false)} />}
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

function Palette({ onDone }: { onDone: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const { user } = useAuth();
  const places = usePlaces();
  const setHelp = useShortcutPanels((s) => s.setHelp);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  const { data: usersData } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });
  const { data: channelsData } = useQuery({ queryKey: ['channels'], queryFn: () => channelsApi.list() });
  // Everything the caller may see; the server takes no scope for that.
  const { data: tasksData } = useQuery({ queryKey: ['tasks', 'palette'], queryFn: () => tasksApi.list(), staleTime: 30_000 });

  const go = (path: string) => () => {
    onDone();
    navigate(path);
  };

  const items = useMemo<Item[]>(() => {
    const channels: any[] = channelsData?.data ?? [];
    const pages: Item[] = [
      { key: 'p-chat', group: 'Pages', label: 'Chat', icon: icon(MessageSquare), run: go('/chat') },
      { key: 'p-calls', group: 'Pages', label: 'Calls', icon: icon(Video), run: go('/calls') },
      ...(places.notes ? [{ key: 'p-notes', group: 'Pages' as const, label: 'Meeting notes', icon: icon(NotebookPen), run: go('/meeting-notes') }] : []),
      { key: 'p-calendar', group: 'Pages', label: 'Calendar', icon: icon(CalendarDays), run: go('/calendar') },
      { key: 'p-tasks', group: 'Pages', label: 'Tasks', icon: icon(CheckSquare), run: go('/tasks') },
      { key: 'p-projects', group: 'Pages', label: 'Projects', icon: icon(FolderGit2), run: go('/projects') },
      ...(places.organization ? [{ key: 'p-org', group: 'Pages' as const, label: 'Organization', icon: icon(Users), run: go('/organization') }] : []),
      { key: 'p-leave', group: 'Pages', label: 'Leave', icon: icon(Plane), run: go('/leave') },
      { key: 'p-settings', group: 'Pages', label: 'Settings', icon: icon(Settings), run: go('/settings') },
      {
        key: 'p-keys',
        group: 'Pages',
        label: 'Keyboard shortcuts',
        icon: icon(Keyboard),
        run: () => setHelp(true),
      },
    ];

    const channelItem = (c: any, group: Item['group']): Item => ({
      key: `${group}-${c.id}`,
      group,
      label: c.name || 'Unnamed',
      detail: c.unreadCount > 0 ? `${c.unreadCount > 9 ? '9+' : c.unreadCount} new` : c.type === 'dm' ? 'Direct message' : undefined,
      icon:
        c.type === 'dm' ? (
          <PersonAvatar userId={c.counterpartId} name={c.name} className="h-6 w-6" fallbackClassName="text-[10px]" decorative />
        ) : (
          icon(c.type === 'announcement' ? Megaphone : Hash)
        ),
      run: go(`/chat/${c.id}`),
    });

    const term = query.trim();
    if (!term) {
      const unread = channels.filter((c) => c.unreadCount > 0).slice(0, PER_GROUP).map((c) => channelItem(c, 'Unread'));
      return [...unread, ...pages];
    }

    const people: any[] = (usersData?.data ?? []).filter((u: any) => u.id !== user?.id && u.isActive !== false);
    const openPerson = (person: any) => async () => {
      // Their conversation if there is one already; the server makes it otherwise.
      const existing = channels.find((c) => c.type === 'dm' && c.counterpartId === person.id);
      if (existing) return go(`/chat/${existing.id}`)();
      try {
        const { data } = await channelsApi.openDirect(person.id);
        go(`/chat/${data.id}`)();
      } catch (err) {
        toast.error(apiErrorMessage(err, 'Could not open that conversation.'));
      }
    };
    const tasks: any[] = tasksData?.data ?? [];

    return [
      ...rank(pages, term, (p) => [p.label]),
      ...rank(people, term, (u) => [u.name, u.email, u.jobTitle])
        .slice(0, PER_GROUP)
        .map<Item>((u) => ({
          key: `u-${u.id}`,
          group: 'People',
          label: u.name || u.email,
          detail: u.jobTitle || u.email,
          icon: <PersonAvatar userId={u.id} name={u.name} src={u.avatarUrl} className="h-6 w-6" fallbackClassName="text-[10px]" decorative />,
          run: openPerson(u),
        })),
      ...rank(
        channels.filter((c) => c.type !== 'dm'),
        term,
        (c) => [c.name]
      )
        .slice(0, PER_GROUP)
        .map((c) => channelItem(c, 'Channels')),
      ...rank(tasks, term, (t) => [t.title, t.key])
        .slice(0, PER_GROUP)
        .map<Item>((t) => ({
          key: `t-${t.id}`,
          group: 'Tasks',
          label: t.title,
          detail: t.key || undefined,
          icon: icon(CheckSquare),
          run: go(`/tasks/${t.id}`),
        })),
    ];
    // go and openPerson are remade each render but only close over navigate and onDone.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, usersData, channelsData, tasksData, places.notes, places.organization, user?.id]);

  // A new list starts at its top.
  useEffect(() => setActive(0), [query]);
  const current = Math.min(active, Math.max(items.length - 1, 0));

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-index="${current}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [current]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (items.length === 0) return;
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setActive((current + step + items.length) % items.length);
    } else if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void items[current]?.run();
    }
  };

  return (
    <>
      <div className="flex items-center gap-2 border-b border-border px-3">
        <Search className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Search people, channels, tasks and pages"
          aria-label="Search people, channels, tasks and pages"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-results"
          aria-activedescendant={items.length > 0 ? `palette-item-${current}` : undefined}
          aria-autocomplete="list"
          className="h-12 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
        />
        <span className="hidden sm:inline-flex">
          <Kbd>Esc</Kbd>
        </span>
      </div>

      <div ref={listRef} id="palette-results" role="listbox" aria-label="Results" className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {items.length === 0 && <p className="px-3 py-6 text-center text-sm text-muted-foreground">Nothing matches &ldquo;{query.trim()}&rdquo;.</p>}
        {items.map((item, i) => (
          <div key={item.key}>
            {(i === 0 || items[i - 1].group !== item.group) && (
              <p className="px-2 pb-1 pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground" aria-hidden="true">
                {item.group}
              </p>
            )}
            <div
              id={`palette-item-${i}`}
              data-index={i}
              role="option"
              aria-selected={i === current}
              onMouseMove={() => i !== current && setActive(i)}
              onClick={() => void item.run()}
              className={cn(
                'flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 text-sm',
                i === current ? 'bg-accent text-accent-foreground' : 'text-foreground'
              )}
            >
              <span className="flex h-6 w-6 shrink-0 items-center justify-center">{item.icon}</span>
              <span className="min-w-0 flex-1 truncate">{item.label}</span>
              {item.detail && <span className="max-w-[40%] shrink-0 truncate text-xs text-muted-foreground">{item.detail}</span>}
            </div>
          </div>
        ))}
      </div>

      <div className="hidden items-center gap-3 border-t border-border px-3 py-2 text-xs text-muted-foreground sm:flex">
        <span className="inline-flex items-center gap-1">
          <Kbd>{'↑'}</Kbd>
          <Kbd>{'↓'}</Kbd> to move
        </span>
        <span className="inline-flex items-center gap-1">
          <Kbd>Enter</Kbd> to open
        </span>
      </div>
    </>
  );
}
