import { createBrowserRouter, redirect, Navigate } from 'react-router-dom';
import { Layout } from '@/features/layout/Layout';
import { LoginPage } from '@/features/auth/LoginPage';
import { WizardPage } from '@/features/wizard/WizardPage';
import { ChatPage } from '@/features/chat/ChatPage';
import { CallsPage } from '@/features/calls/CallsPage';
import { TasksPage } from '@/features/tasks/TasksPage';
import { SettingsPage } from '@/features/settings/SettingsPage';
import { CallViewPage } from '@/features/calls/CallViewPage';
import { TaskDetailPage } from '@/features/tasks/TaskDetailPage';
import { useAuthStore } from '@/shared/store/authStore';

const requireAuth = async () => {
  const { isAuthenticated } = useAuthStore.getState();
  if (!isAuthenticated) return redirect('/login');
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
const requireSetupThenAuth = async () => {
  const setupRedirect = await requireSetup();
  if (setupRedirect) return setupRedirect;
  return requireAuth();
};

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <LoginPage />,
    errorElement: <ErrorPage />,
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
      { path: 'tasks', element: <TasksPage /> },
      { path: 'tasks/:taskId', element: <TaskDetailPage /> },
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
]);

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
