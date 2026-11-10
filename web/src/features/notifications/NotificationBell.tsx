import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell, CheckCheck, Loader2, X, BellRing } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import { notificationsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';
import { useToast } from '@/shared/hooks/useToast';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { formatRelativeTime, cn } from '@/shared/utils/cn';
import { refreshTasksFor } from '@/features/tasks/liveQuery';
import { dayLabelKey } from '@/features/calendar/layout';

interface NotificationItem {
  id: string;
  type: string;
  title: string;
  body: string;
  data?: Record<string, any>;
  readAt?: string | null;
  createdAt: string;
}

/** The calendar, on the day the meeting is, when the notification says which. */
function calendarLink(startsAt?: string) {
  const date = startsAt ? new Date(startsAt) : null;
  return date && !Number.isNaN(date.getTime()) ? `/calendar?date=${dayLabelKey(date)}` : '/calendar';
}

/**
 * The bell badge was a hardcoded number and there was no notification list at
 * all, even though notifications were already being stored and pushed over the
 * socket. This connects the two.
 */
export function NotificationBell() {
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();
  const { on } = useWebSocket();

  const { data: countData } = useQuery({
    queryKey: ['notifications', 'unread-count'],
    queryFn: () => notificationsApi.unreadCount(),
    refetchInterval: 60_000,
  });
  const unread: number = countData?.data?.count ?? 0;

  const { data, isLoading } = useQuery({
    queryKey: ['notifications'],
    queryFn: () => notificationsApi.list({ limit: 30 }),
  });
  const notifications: NotificationItem[] = data?.data ?? [];

  // A notification arriving while the tab is open should appear immediately
  // rather than waiting for the next poll.
  useEffect(() => {
    return on('notification.new', (msg) => {
      queryClient.invalidateQueries({ queryKey: ['notifications'] });
      queryClient.invalidateQueries({ queryKey: ['notifications', 'unread-count'] });
      refreshTasksFor(queryClient, msg.payload as { data?: { taskId?: string } });
    });
  }, [on, queryClient]);

  const { permission, request } = useDesktopNotifications();
  const [menuOpen, setMenuOpen] = useState(false);

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['notifications'] });
    queryClient.invalidateQueries({ queryKey: ['notifications', 'unread-count'] });
  };

  const open = async (n: NotificationItem) => {
    // Left open, the menu covers the page it just took you to and keeps it from being
    // clicked or read until it is dismissed.
    setMenuOpen(false);
    try {
      if (!n.readAt) await notificationsApi.markRead(n.id);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not mark that as read.'));
    }
    // Link through to whatever the notification is about.
    const taskId = n.data?.taskId;
    const channelId = n.data?.channelId;
    if (taskId) navigate(`/tasks/${taskId}`);
    else if (channelId) navigate(`/chat/${channelId}`);
    else if (n.type.startsWith('meeting.')) navigate(calendarLink(n.data?.startsAt));
    else if (n.type === 'call.knock' && n.data?.callId) navigate(`/calls/${n.data.callId}`);
    else if (n.type.startsWith('minutes.') && n.data?.callId) navigate(`/calls/${n.data.callId}/minutes`);
    // A pull request no task names opens on GitHub.
    else if (n.type.startsWith('code.') && typeof n.data?.url === 'string' && /^https:\/\//.test(n.data.url)) {
      window.open(n.data.url, '_blank', 'noopener');
    }
  };

  const markAll = async () => {
    try {
      await notificationsApi.markAllRead();
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not mark everything as read.'));
    }
  };

  const archive = async (id: string) => {
    try {
      await notificationsApi.archive(id);
      refresh();
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not dismiss that notification.'));
    }
  };

  return (
    <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="relative" aria-label={`Notifications${unread ? `, ${unread} unread` : ''}`}>
          <Bell className="h-5 w-5" aria-hidden="true" />
          {unread > 0 && (
            <span className="absolute -top-1 -right-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-medium text-destructive-foreground">
              {unread > 99 ? '99+' : unread}
            </span>
          )}
        </Button>
      </DropdownMenuTrigger>

      <DropdownMenuContent align="end" className="w-80 p-0">
        <div className="flex items-center justify-between border-b border-border px-3 py-2">
          <span className="text-sm font-medium">Notifications</span>
          {unread > 0 && (
            <Button variant="ghost" size="sm" onClick={markAll} className="h-7 text-xs">
              <CheckCheck className="mr-1 h-3 w-3" />
              Mark all read
            </Button>
          )}
        </div>

        {/* Browsers only grant this in response to something the user did,
            so there has to be somewhere to ask. */}
        {permission === 'default' && (
          <button
            onClick={async () => {
              const result = await request();
              if (result === 'granted') toast.success('Notifications are on.');
              else if (result === 'denied') {
                toast.error('Your browser blocked notifications for this site.');
              }
            }}
            className="flex w-full items-center gap-2 border-b border-border px-3 py-2 text-left text-sm hover:bg-accent"
          >
            <BellRing className="h-4 w-4 shrink-0 text-primary" aria-hidden="true" />
            <span>
              <span className="block font-medium">Turn on desktop notifications</span>
              <span className="block text-xs text-muted-foreground">
                Be told about messages while TeamUp is open in another tab.
              </span>
            </span>
          </button>
        )}
        {permission === 'denied' && (
          <p className="border-b border-border px-3 py-2 text-xs text-muted-foreground">
            Desktop notifications are blocked for this site. Allow them in your
            browser's settings to be told about messages in other tabs.
          </p>
        )}

        <div className="max-h-96 overflow-y-auto">
          {isLoading && (
            <div className="flex items-center justify-center p-6 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
            </div>
          )}

          {!isLoading && notifications.length === 0 && (
            <div className="p-6 text-center">
              <Bell className="mx-auto mb-2 h-6 w-6 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">Nothing yet.</p>
              <p className="text-xs text-muted-foreground">
                You will hear about mentions and tasks assigned to you.
              </p>
            </div>
          )}

          {notifications.map((n) => (
            <div
              key={n.id}
              className={cn(
                'group flex items-start gap-2 border-b border-border px-3 py-2 last:border-0',
                !n.readAt && 'bg-accent/40'
              )}
            >
              <button onClick={() => open(n)} className="min-w-0 flex-1 text-left">
                <p className="truncate text-sm font-medium">{n.title}</p>
                <p className="truncate text-xs text-muted-foreground">{n.body}</p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">{formatRelativeTime(n.createdAt)}</p>
              </button>
              <button
                onClick={() => archive(n.id)}
                aria-label="Dismiss"
                className="mt-1 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-accent group-hover:opacity-100"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          ))}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
