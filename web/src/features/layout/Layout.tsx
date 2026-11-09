import { Navigate, Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { BottomNav } from "./BottomNav";
import { Header } from "./Header";
import { useUIStore } from "@/shared/store";
import { useAuthStore } from "@/shared/store/authStore";
import { IncomingCallDialog } from "@/features/calls/IncomingCallDialog";
import { ActiveCallProvider } from "@/features/calls/ActiveCallProvider";
import { UpdateOffer } from "@/sw/UpdateOffer";
import { KnockCenter } from "@/features/calls/KnockCenter";
import { ChatNotifications } from "@/features/chat/ChatNotifications";
import { usePushSubscription } from "@/features/notifications/usePushSubscription";
import { cn } from "@/shared/utils/cn";
import { OutageBanner } from "@/features/status/OutageBanner";
import { TopProgress } from "@/shared/components/TopProgress";
import { WelcomeTour } from "@/features/tour/WelcomeTour";
import { InstallBanner } from "@/features/install/InstallApp";

export function Layout() {
  const sidebarOpen = useUIStore((s) => s.sidebarOpen);
  // A push subscription can be dropped by the browser at any time, and going
  // silently deaf is the worst way for notifications to fail. Somebody who has
  // already agreed to them is re-registered here on every load.
  usePushSubscription();
  const user = useAuthStore((s) => s.user);

  // The API refuses every one of these pages' requests until the password is
  // replaced, so showing the app at all would only render a wall of errors.
  if (user?.mustChangePassword)
    return <Navigate to="/change-password" replace />;

  return (
    <ActiveCallProvider>
      <TopProgress />
      {/* Installed on a phone the app reaches the screen's edges (viewport-fit in
          index.html), so it keeps clear of the status bar and, sideways, the notch.
          The bottom is the bottom navigation's to keep clear of. */}
      <div className="flex h-viewport overflow-hidden bg-background pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)]">
        <Sidebar />
        {/* The sidebar is position:fixed, so it is out of the flex flow and the
          content column has to be offset by hand - otherwise every page renders
          underneath it and its left edge is unclickable. Only on a large
          screen: below that there is no sidebar to make room for. */}
        <div
          className={cn(
            "flex min-w-0 flex-1 flex-col overflow-hidden transition-all duration-200",
            sidebarOpen ? "lg:ml-64" : "lg:ml-16",
          )}
        >
          <Header />
          {/* While something is down, said before anybody has to ask. */}
          <OutageBanner />
          {/* Pages render their own header and padding, so main stays bare;
            padding here produced a second layer of chrome. */}
          <main className="min-h-0 flex-1 overflow-hidden">
            <Outlet />
          </main>
          {/* On a phone the four sections sit along the bottom. It is a sibling
              of the page rather than something floating over it, so a page that
              fills the screen still ends above it. */}
          <BottomNav />
        </div>
        {/* A call has to reach you wherever you are, not only on the calls page. */}
        <IncomingCallDialog />
        {/* Somebody asking to join reaches every member, in the call or not. */}
        <KnockCenter />
      {/* A message has to reach you wherever you are too, not only on the
          page that happens to be about chat. */}
      <ChatNotifications />
        <UpdateOffer />
        {/* A look around for somebody signing in for the first time. */}
        <WelcomeTour />
        {/* On a phone, a suggestion to put TeamUp on the home screen. */}
        <InstallBanner />
      </div>
    </ActiveCallProvider>
  );
}
