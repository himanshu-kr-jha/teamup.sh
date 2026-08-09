import { Menu, Sun, Moon } from 'lucide-react';
import { usePresence } from '@/features/presence/usePresence';
import { AvailabilityButton } from '@/features/presence/AvailabilityPicker';
import { useAuth } from '@/features/auth/AuthProvider';
import { Button } from '@/shared/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Avatar, AvatarFallback, AvatarImage } from '@/shared/components/ui/avatar';
import { NotificationBell } from '@/features/notifications/NotificationBell';
import { SearchBox } from '@/features/search/SearchBox';
import { useState } from 'react';

interface HeaderProps {
  onMenuClick: () => void;
}

export function Header({ onMenuClick }: HeaderProps) {
  const { user, logout } = useAuth();
  const { personOf } = usePresence();
  const [theme, setTheme] = useState<'light' | 'dark'>('light');

  return (
    // z-50, above the channel list: sticky positioning makes this a stacking
    // context, so the search results inside it could never rise above their
    // parent no matter what z-index they carried — they came out behind the
    // conversations.
    <header className="sticky top-0 z-50 flex h-16 items-center gap-4 border-b border-border bg-background/95 px-4 backdrop-blur supports-[backdrop-filter]:bg-background/60 md:px-6">
      <button
        onClick={onMenuClick}
        className="lg:hidden p-2 rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
        aria-label="Toggle menu"
      >
        <Menu className="h-5 w-5" aria-hidden="true" />
      </button>

      <div className="flex-1 flex items-center gap-4">
        <SearchBox />
      </div>

      <div className="flex items-center gap-2">
        <AvailabilityButton current={personOf(user?.id)} />
        {/* Theme toggle */}
        <Button
          variant="ghost"
          size="icon"
          onClick={() => {
            const newTheme = theme === 'light' ? 'dark' : 'light';
            setTheme(newTheme);
            document.documentElement.classList.toggle('dark', newTheme === 'dark');
            localStorage.setItem('theme', newTheme);
          }}
          aria-label={theme === 'light' ? 'Switch to dark mode' : 'Switch to light mode'}
        >
          {theme === 'light' ? <Moon className="h-5 w-5" /> : <Sun className="h-5 w-5" />}
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
            <DropdownMenuItem onSelect={() => {}}>Profile</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => {}}>Settings</DropdownMenuItem>
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
