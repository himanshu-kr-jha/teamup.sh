import { Link, NavLink } from 'react-router-dom';
import { CalendarDays, MessageSquare, Video, CheckSquare, Settings, Users, ChevronLeft, ChevronRight } from 'lucide-react';
import { useUIStore } from '@/shared/store';
import { useAuth } from '@/features/auth/AuthProvider';
import { cn } from '@/shared/utils/cn';
import { useOrganizationAccess } from './useOrganizationAccess';
import { BrandMark } from '@/features/branding/BrandMark';
import { UserAvatar } from '@/shared/components/UserAvatar';
import { badgeText, useUnreadChatCount } from '@/features/chat/useUnreadChats';

export function Sidebar() {
  const { sidebarOpen, toggleSidebar } = useUIStore();
  const { user, isAuthenticated, isLoading } = useAuth();

  // A member cannot change anything on the Organization screen, so it is not
  // offered to them; administrators and whoever runs a team still have it.
  const { allowed: mayAdminister } = useOrganizationAccess();
  const unreadChats = useUnreadChatCount();

  const navItems = [
    { path: '/chat', label: 'Chat', icon: MessageSquare },
    { path: '/calls', label: 'Calls', icon: Video },
    { path: '/calendar', label: 'Calendar', icon: CalendarDays },
    { path: '/tasks', label: 'Tasks', icon: CheckSquare },
    ...(mayAdminister ? [{ path: '/organization', label: 'Organization', icon: Users }] : []),
    { path: '/settings', label: 'Settings', icon: Settings },
  ];

  return (
    <aside
      className={cn(
        // Not on a phone at all any more. It used to be a drawer that covered
        // the page, which is what the sections along the bottom replaced; two
        // ways to the same four places, one of them hiding what you were
        // looking at, is worse than one.
        'fixed left-0 top-0 z-50 hidden h-viewport border-r border-border bg-card transition-all duration-200 lg:block',
        sidebarOpen ? 'w-64' : 'w-16'
      )}
      aria-label="Main navigation"
    >
      <div className="flex h-full flex-col">
        {/* Logo */}
        <div className={cn('flex h-16 items-center justify-between px-4 border-b border-border', !sidebarOpen && 'justify-center')}>
          <Link to="/chat" className="flex min-w-0 items-center gap-2" aria-label="Home">
            <BrandMark showName={sidebarOpen} />
          </Link>
          <button
            onClick={toggleSidebar}
            className={cn(
              'rounded-md p-2 text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors',
              !sidebarOpen && 'mx-auto'
            )}
            aria-label={sidebarOpen ? 'Collapse sidebar' : 'Expand sidebar'}
            aria-expanded={sidebarOpen}
          >
            {sidebarOpen ? <ChevronLeft className="h-5 w-5" /> : <ChevronRight className="h-5 w-5" />}
          </button>
        </div>

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto p-2 space-y-1" aria-label="Main">
          {navItems.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors',
                  isActive
                    ? 'bg-primary text-primary-foreground'
                    : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                  !sidebarOpen && 'justify-center px-2'
                )
              }
            >
              {({ isActive }) => {
                const badge = item.path === '/chat' && unreadChats > 0 ? unreadChats : 0;
                return (
                  <>
                    <span className="relative flex-shrink-0">
                      <item.icon className="h-5 w-5" aria-hidden="true" />
                      {badge > 0 && !sidebarOpen && (
                        <span
                          className="absolute -right-2 -top-2 flex h-4 min-w-[1rem] items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold leading-none text-destructive-foreground"
                          data-testid="chat-unread-badge"
                        >
                          {badgeText(badge)}
                        </span>
                      )}
                    </span>
                    {sidebarOpen && <span className="flex-1">{item.label}</span>}
                    {badge > 0 && sidebarOpen && (
                      <span
                        className={cn(
                          'flex h-5 min-w-[1.25rem] items-center justify-center rounded-full px-1.5 text-xs font-semibold',
                          isActive ? 'bg-primary-foreground/20 text-primary-foreground' : 'bg-destructive text-destructive-foreground'
                        )}
                        data-testid="chat-unread-badge"
                      >
                        {badgeText(badge)}
                      </span>
                    )}
                    {badge > 0 && (
                      <span className="sr-only">
                        , {badge === 1 ? '1 chat with unread messages' : `${badge} chats with unread messages`}
                      </span>
                    )}
                  </>
                );
              }}
            </NavLink>
          ))}


        </nav>

        {/* User status - only show when authenticated and sidebar open */}
        {sidebarOpen && !isLoading && isAuthenticated && user && (
          <div className="p-4 border-t border-border">
            <div className="flex items-center gap-3">
              <div className="relative">
                <UserAvatar name={user.name} src={user.avatarUrl} className="h-10 w-10" fallbackClassName="bg-primary/20 text-primary" />
                <span className="absolute bottom-0 right-0 h-3 w-3 rounded-full bg-green-500 border-2 border-card" aria-label="Online" />
              </div>
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium truncate">{user.name}</p>
                <p className="text-xs text-muted-foreground truncate">{user.email}</p>
              </div>
            </div>
          </div>
        )}
      </div>
    </aside>
  );
}
