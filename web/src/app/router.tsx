import { createBrowserRouter, redirect, Navigate } from 'react-router-dom';
import { Layout } from '@/features/layout/Layout';
import { LoginPage } from '@/features/auth/LoginPage';
import { ChangePasswordPage } from '@/features/auth/ChangePasswordPage';
import { WizardPage } from '@/features/wizard/WizardPage';
import { ChatPage } from '@/features/chat/ChatPage';
import { CallsPage } from '@/features/calls/CallsPage';
import { TasksPage } from '@/features/tasks/TasksPage';
import { SettingsPage } from '@/features/settings/SettingsPage';
import { AdminPage } from '@/features/admin/AdminPage';
import { CallViewPage } from '@/features/calls/CallViewPage';
import { MinutesPage } from '@/features/calls/MinutesPage';
import { MeetingNotesPage } from '@/features/calls/MeetingNotesPage';
import { AttendancePage } from '@/features/calls/standing/AttendancePage';
import { CalendarPage } from '@/features/calendar/CalendarPage';
import { StatusPage } from '@/features/status/StatusPage';
import { TaskDetailPage } from '@/features/tasks/TaskDetailPage';
import { useAuthStore } from '@/shared/store/authStore';
import { loginPathFor } from '@/features/auth/nextPath';
import { restoreSession } from '@/features/auth/session';
import { useOrganizationAccess } from '@/features/layout/useOrganizationAccess';

const requireAuth = async (request: Request) => {
  const { isAuthenticated, setUser } = useAuthStore.getState();
  // What this browser remembers is only a hint: the session is in the cookie, so
  // somebody whose local storage was cleared is still signed in.
  if (!isAuthenticated && !(await restoreSession(setUser))) {
    const { pathname, search } = new URL(request.url);
    return redirect(loginPathFor(pathname, search));
  }
  return null;
};

const requireSetup = async () => {
  try {
    const res = await fetch('/api/wizard/status');
    if (res.ok) {
      const data = await res.json();
      if (!data.complete) return redirect('/wizard/step/1');
    }
  } catch {
    // If wizard status fails, allow access (might be already set up)
  }
  return null;
};

// React Router only acts on a Response returned by the loader. Returning
// Promise.all([...]) handed it an array, so both redirects were silently
// dropped and a brand-new instance rendered the empty app instead of the
// wizard. Setup is checked first: logging in is impossible before it runs.
const requireSetupThenAuth = async ({ request }: { request: Request }) => {
  const setupRedirect = await requireSetup();
  if (setupRedirect) return setupRedirect;
  return requireAuth(request);
};

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <LoginPage />,
    errorElement: <ErrorPage />,
  },
  // Open to anybody: whether something is down matters most when signing in is
  // the thing that does not work.
  {
    path: '/status',
    element: <StatusPage />,
    errorElement: <ErrorPage />,
  },
  {
    path: '/change-password',
    element: <ChangePasswordPage />,
    errorElement: <ErrorPage />,
    loader: requireSetupThenAuth,
  },
  {
    path: '/wizard',
    element: <Navigate to="/wizard/step/1" replace />,
    errorElement: <ErrorPage />,
  },
  {
    path: '/wizard/step/:step',
    element: <WizardPage />,
    errorElement: <ErrorPage />,
  },
  {
    path: '/',
    element: <Layout />,
    errorElement: <ErrorPage />,
    loader: requireSetupThenAuth,
    children: [
      { index: true, element: <Navigate to="/chat" replace /> },
      { path: 'chat', element: <ChatPage /> },
      { path: 'chat/:channelId', element: <ChatPage /> },
      { path: 'calls', element: <CallsPage /> },
      { path: 'calls/:roomName', element: <CallViewPage /> },
      { path: 'calls/:callId/minutes', element: <MinutesPage /> },
      { path: 'meeting-notes', element: <MeetingNotesPage /> },
      { path: 'calls/standing/:id/attendance', element: <AttendancePage /> },
      { path: 'calendar', element: <CalendarPage /> },
      { path: 'tasks', element: <TasksPage /> },
      { path: 'tasks/:taskId', element: <TaskDetailPage /> },
      { path: 'settings', element: <SettingsPage /> },
      { path: 'organization', element: <OnlyForAdministrators /> },
    ],
  },
]);

/**
 * Taking the link out of the navigation is not the same as closing the door:
 * the address still works, and anybody who has been there once has it in their
 * history. Turned away rather than hidden.
 *
 * Nothing is decided until the answer is in, or a team's manager arriving from
 * a bookmark would be bounced before we knew they belonged there.
 */
function OnlyForAdministrators() {
  const { allowed, resolved } = useOrganizationAccess();
  if (!resolved) return null;
  if (!allowed) return <Navigate to="/chat" replace />;
  return <AdminPage />;
}

function ErrorPage() {
  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <div className="text-center">
        <h1 className="text-4xl font-bold text-destructive">Error</h1>
        <p className="mt-2 text-muted-foreground">Something went wrong</p>
      </div>
    </div>
  );
}
