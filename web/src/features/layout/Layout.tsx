import { Navigate, Outlet } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { Header } from './Header';
import { useUIStore } from '@/shared/store';
import { useAuthStore } from '@/shared/store/authStore';
import { IncomingCallDialog } from '@/features/calls/IncomingCallDialog';
import { cn } from '@/shared/utils/cn';

export function Layout() {
  const { sidebarOpen, toggleSidebar } = useUIStore();
  const user = useAuthStore((s) => s.user);

  // The API refuses every one of these pages' requests until the password is
  // replaced, so showing the app at all would only render a wall of errors.
  if (user?.mustChangePassword) return <Navigate to="/change-password" replace />;

  return (
    <div className="flex h-screen bg-background">
      <Sidebar />
      {/* The sidebar is position:fixed, so it is out of the flex flow and the
          content column has to be offset by hand — otherwise every page renders
          underneath it and its left edge is unclickable. */}
      <div
        className={cn(
          'flex flex-1 flex-col overflow-hidden transition-all duration-200',
          sidebarOpen ? 'lg:ml-64' : 'lg:ml-16'
        )}
      >
        <Header onMenuClick={toggleSidebar} />
        {/* Pages render their own header and padding, so main stays bare;
            padding here produced a second layer of chrome. */}
        <main className="flex-1 overflow-hidden">
          <Outlet />
        </main>
      </div>
      {/* A call has to reach you wherever you are, not only on the calls page. */}
      <IncomingCallDialog />
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/50 lg:hidden"
          onClick={toggleSidebar}
          aria-hidden="true"
        />
      )}
    </div>
  );
}
