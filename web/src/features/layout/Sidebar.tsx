import { Link, NavLink } from 'react-router-dom';
import { MessageSquare, Video, CheckSquare, Settings, Users, ChevronLeft, ChevronRight, Hash, User } from 'lucide-react';
import { useUIStore } from '@/shared/store';
import { useAuth } from '@/features/auth/AuthProvider';
import { cn } from '@/shared/utils/cn';
import { useOrganizationAccess } from './useOrganizationAccess';

export function Sidebar() {
  const { sidebarOpen, toggleSidebar } = useUIStore();
  const { user, isAuthenticated, isLoading } = useAuth();

  // A member cannot change anything on the Organization screen, so it is not
  // offered to them; administrators and whoever runs a team still have it.
  const { allowed: mayAdminister } = useOrganizationAccess();

  const navItems = [
    { path: '/chat', label: 'Chat', icon: MessageSquare },
    { path: '/calls', label: 'Calls', icon: Video },
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
          <Link to="/chat" className="flex items-center gap-2" aria-label="TeamUp Home">
            <Hash className="h-8 w-8 text-primary" />
            {sidebarOpen && <span className="font-bold text-xl">TeamUp</span>}
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
              <item.icon className="h-5 w-5 flex-shrink-0" aria-hidden="true" />
              {sidebarOpen && <span>{item.label}</span>}
            </NavLink>
          ))}


        </nav>

        {/* User status - only show when authenticated and sidebar open */}
        {sidebarOpen && !isLoading && isAuthenticated && user && (
          <div className="p-4 border-t border-border">
            <div className="flex items-center gap-3">
              <div className="relative">
                <div className="h-10 w-10 rounded-full bg-primary/20 flex items-center justify-center">
                  <User className="h-5 w-5 text-primary" aria-hidden="true" />
                </div>
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
