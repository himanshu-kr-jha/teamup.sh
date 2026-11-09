import { useQueryClient } from '@tanstack/react-query';
import { AtSign, Bell, BellOff, Check } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import { channelsApi, type NotifyLevel } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

const LEVELS: { level: NotifyLevel; label: string; hint: string; Icon: typeof Bell }[] = [
  { level: 'all', label: 'All messages', hint: 'A sound and a notification for every message', Icon: Bell },
  { level: 'mentions', label: 'Only @mentions', hint: 'Only when somebody mentions you', Icon: AtSign },
  { level: 'none', label: 'Nothing', hint: 'No sound or notification, mentions included', Icon: BellOff },
];

/**
 * The bell in a conversation's header: what it interrupts you with. Unread counts
 * stay whatever you choose. Only members have a choice; somebody reading a public
 * channel they have not joined is told to join first.
 */
export function NotifyMenu({ channelId, level }: { channelId?: string; level?: NotifyLevel | null }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const current = LEVELS.find((l) => l.level === level) ?? LEVELS[0];
  const member = !!channelId && level != null;
  const label = member ? `Notifications: ${current.label}` : 'Join this channel to choose its notifications';

  const choose = async (next: NotifyLevel) => {
    if (!channelId || next === level) return;
    const set = (value: NotifyLevel | null | undefined) =>
      queryClient.setQueryData(['channels'], (old: any) =>
        old?.data ? { ...old, data: old.data.map((c: any) => (c.id === channelId ? { ...c, notifyLevel: value } : c)) } : old
      );
    // At once, and put back if the server says no.
    set(next);
    try {
      await channelsApi.setNotifyLevel(channelId, next);
      toast.success(
        next === 'all'
          ? 'You will be notified of every message here.'
          : next === 'mentions'
            ? 'You will only be notified when somebody mentions you here.'
            : 'Notifications for this conversation are off.'
      );
    } catch (err) {
      set(level);
      toast.error(apiErrorMessage(err, 'Could not change the notifications.'));
    }
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={!member}>
        <Button variant="ghost" size="icon" aria-label={label} title={label} data-testid="notify-menu">
          <current.Icon className={current.level === 'none' ? 'h-5 w-5 text-muted-foreground' : 'h-5 w-5'} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Notify me about</DropdownMenuLabel>
        {LEVELS.map(({ level: l, label: text, hint, Icon }) => (
          <DropdownMenuItem
            key={l}
            role="menuitemradio"
            aria-checked={l === current.level}
            onSelect={() => void choose(l)}
            className="items-start gap-3 py-2"
          >
            <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 flex-1">
              <span className="block font-medium">{text}</span>
              <span className="block text-xs text-muted-foreground">{hint}</span>
            </span>
            <Check className={l === current.level ? 'mt-0.5 h-4 w-4 shrink-0 text-primary' : 'mt-0.5 h-4 w-4 shrink-0 opacity-0'} aria-hidden="true" />
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
