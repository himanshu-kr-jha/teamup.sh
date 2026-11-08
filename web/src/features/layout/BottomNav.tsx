import { NavLink } from 'react-router-dom';
import { CalendarDays, MessageSquare, Video, CheckSquare, Settings } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { badgeText, useUnreadChatCount } from '@/features/chat/useUnreadChats';

/**
 * The places worth going, along the bottom of a phone.
 *
 * Getting anywhere used to mean opening the drawer, which covers the page, and
 * choosing from it - two taps and a moment of not being able to see where you
 * were. These are one tap and always visible, at the end of the screen a thumb
 * actually reaches.
 *
 * Organization is deliberately not here. It is for administrators, it is the
 * one destination most people never need, and five tabs is as many as fit a
 * narrow screen; the drawer still has it for whoever it is for.
 */
const tabs = [
  { path: '/chat', label: 'Chat', icon: MessageSquare },
  { path: '/calls', label: 'Calls', icon: Video },
  { path: '/calendar', label: 'Calendar', icon: CalendarDays },
  { path: '/tasks', label: 'Tasks', icon: CheckSquare },
  { path: '/settings', label: 'Settings', icon: Settings },
];

export function BottomNav() {
  const unreadChats = useUnreadChatCount();
  return (
    <nav
      // Part of the page's column rather than floating over it, so nothing ends
      // up underneath it. The padding is for the home indicator on phones that
      // have one, and collapses to nothing on those that do not.
      className="shrink-0 border-t border-border bg-card pb-[env(safe-area-inset-bottom)] lg:hidden"
      aria-label="Sections"
    >
      <div className="flex items-stretch">
        {tabs.map((tab) => (
          <NavLink
            key={tab.path}
            to={tab.path}
            className={({ isActive }) =>
              cn(
                // A 56px target: comfortably tappable without eating the screen.
                'flex flex-1 flex-col items-center justify-center gap-0.5 py-2 text-[11px] font-medium transition-colors',
                isActive ? 'text-primary' : 'text-muted-foreground'
              )
            }
          >
            {({ isActive }) => (
              <>
                <span className="relative">
                  <tab.icon
                    className={cn('h-5 w-5', isActive && 'fill-primary/10')}
                    aria-hidden="true"
                  />
                  {tab.path === '/chat' && unreadChats > 0 && (
                    <span
                      className="absolute -right-2.5 -top-1.5 flex h-4 min-w-[1rem] items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold leading-none text-destructive-foreground"
                      data-testid="chat-unread-badge"
                    >
                      {badgeText(unreadChats)}
                    </span>
                  )}
                </span>
                <span>{tab.label}</span>
                {tab.path === '/chat' && unreadChats > 0 && (
                  <span className="sr-only">
                    , {unreadChats === 1 ? '1 chat with unread messages' : `${unreadChats} chats with unread messages`}
                  </span>
                )}
              </>
            )}
          </NavLink>
        ))}
      </div>
    </nav>
  );
}
