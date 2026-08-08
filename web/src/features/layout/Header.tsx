import { Bell, Search, Menu, Sun, Moon } from 'lucide-react';
import { useAuth } from '@/features/auth/AuthProvider';
import { Button } from '@/shared/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/shared/components/ui/dropdown-menu';
import { Avatar, AvatarFallback, AvatarImage } from '@/shared/components/ui/avatar';
import { useState } from 'react';

interface HeaderProps {
  onMenuClick: () => void;
}

export function Header({ onMenuClick }: HeaderProps) {
  const { user, logout } = useAuth();
  const [searchQuery, setSearchQuery] = useState('');
  const [theme, setTheme] = useState<'light' | 'dark'>('light');

  return (
    <header className="sticky top-0 z-30 flex h-16 items-center gap-4 border-b border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 px-4 md:px-6">
      <button
        onClick={onMenuClick}
        className="lg:hidden p-2 rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
        aria-label="Toggle menu"
      >
        <Menu className="h-5 w-5" aria-hidden="true" />
      </button>

      <div className="flex-1 flex items-center gap-4">
        <div className="relative w-full max-w-md hidden sm:block">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <input
            type="search"
            placeholder="Search messages, tasks, people..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full h-9 rounded-md bg-secondary pl-10 pr-4 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            aria-label="Search"
          />
        </div>
      </div>

      <div className="flex items-center gap-2">
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

        {/* Notifications */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="relative" aria-label="Notifications">
              <Bell className="h-5 w-5" aria-hidden="true" />
              <span className="absolute -top-1 -right-1 h-4 w-4 rounded-full bg-destructive text-[10px] font-medium text-destructive-foreground flex items-center justify-center">
                3
              </span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-80">
            <div className="p-2 border-b border-border">
              <h3 className="font-medium">Notifications</h3>
            </div>
            <div className="max-h-96 overflow-y-auto">
              <DropdownMenuItem className="p-3 hover:bg-accent focus:bg-accent" onSelect={() => {}}>
                <p className="font-medium">New message in #general</p>
                <p className="text-sm text-muted-foreground">John Doe: "Hey team, meeting in 5 mins"</p>
                <p className="text-xs text-muted-foreground">2 min ago</p>
              </DropdownMenuItem>
              <DropdownMenuItem className="p-3 hover:bg-accent focus:bg-accent" onSelect={() => {}}>
                <p className="font-medium">Task assigned: "Fix login bug"</p>
                <p className="text-sm text-muted-foreground">Assigned by Jane Smith</p>
                <p className="text-xs text-muted-foreground">1 hour ago</p>
              </DropdownMenuItem>
              <DropdownMenuItem className="p-3 hover:bg-accent focus:bg-accent" onSelect={() => {}}>
                <p className="font-medium">Call started: Daily Standup</p>
                <p className="text-sm text-muted-foreground">Join the ongoing call</p>
                <p className="text-xs text-muted-foreground">3 hours ago</p>
              </DropdownMenuItem>
            </div>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-center text-primary" onSelect={() => {}}>
              View all notifications
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

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
