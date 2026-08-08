import { Outlet } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { Header } from './Header';
import { useUIStore } from '@/shared/store';
import { cn } from '@/shared/utils/cn';

export function Layout() {
  const { sidebarOpen, toggleSidebar } = useUIStore();

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
