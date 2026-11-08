import { Sun, Moon } from 'lucide-react';
import { BrandMark } from '@/features/branding/BrandMark';
import { Link, useNavigate } from 'react-router-dom';
import { usePresence } from '@/features/presence/usePresence';
import { AvailabilityButton } from '@/features/presence/AvailabilityPicker';
import { useAuth } from '@/features/auth/AuthProvider';
import { Button } from '@/shared/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Avatar, AvatarFallback, AvatarImage } from '@/shared/components/ui/avatar';
import { NotificationBell } from '@/features/notifications/NotificationBell';
import { SearchBox } from '@/features/search/SearchBox';
import { applyTheme, useIsDark } from '@/shared/theme';
import { useOrganizationAccess } from './useOrganizationAccess';

export function Header() {
  const { user, logout } = useAuth();
  const { personOf } = usePresence();
  const navigate = useNavigate();
  const { allowed: mayAdminister } = useOrganizationAccess();
  const isDark = useIsDark();

  return (
    // z-50, above the channel list: sticky positioning makes this a stacking
    // context, so the search results inside it could never rise above their
    // parent no matter what z-index they carried — they came out behind the
    // conversations.
    <header className="sticky top-0 z-50 flex h-16 items-center gap-4 border-b border-border bg-background/95 px-4 backdrop-blur supports-[backdrop-filter]:bg-background/60 md:px-6">
      <div className="flex flex-1 items-center gap-4">
        {/* Below a large screen there is no sidebar to carry the name, and with
            the menu button gone this corner was simply empty. */}
        <Link to="/chat" className="flex min-w-0 max-w-[40%] items-center lg:hidden" aria-label="Home">
          <BrandMark size="sm" />
        </Link>
        <SearchBox />
      </div>

      <div className="flex items-center gap-2">
        <AvailabilityButton current={personOf(user?.id)} />
        {/* Theme toggle */}
        <Button
          variant="ghost"
          size="icon"
          onClick={() => applyTheme(isDark ? 'light' : 'dark')}
          aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
        >
          {isDark ? <Sun className="h-5 w-5" /> : <Moon className="h-5 w-5" />}
        </Button>

        {/* Notifications: real unread count and list */}
        <NotificationBell />

        {/* User menu */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" className="relative h-9 w-9 rounded-full" aria-label="User menu">
              <Avatar className="h-9 w-9">
                <AvatarImage src={user?.avatarUrl} alt={user?.name} />
                <AvatarFallback>{user?.name?.charAt(0).toUpperCase()}</AvatarFallback>
              </Avatar>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <div className="p-2 border-b border-border">
              <p className="font-medium">{user?.name}</p>
              <p className="text-sm text-muted-foreground truncate">{user?.email}</p>
            </div>
            <DropdownMenuSeparator />
            {/* These did nothing at all before. They are also the only way to
                the Organization screen on a phone, which no longer has a
                drawer to hold it. */}
            <DropdownMenuItem onSelect={() => navigate('/settings')}>Profile</DropdownMenuItem>
            {mayAdminister && (
              <DropdownMenuItem onSelect={() => navigate('/organization')}>Organization</DropdownMenuItem>
            )}
            <DropdownMenuItem onSelect={() => navigate('/status')}>Service status</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={logout} className="text-destructive focus:text-destructive">
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
