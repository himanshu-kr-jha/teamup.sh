import React from 'react';
import ReactDOM from 'react-dom/client';
import { RouterProvider } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactQueryDevtools } from '@tanstack/react-query-devtools';
import { router } from './app/router';
import { AuthProvider } from '@/features/auth/AuthProvider';
import { WSProvider } from '@/features/websocket/WSProvider';
import { Toaster } from '@/shared/components/ui/toaster';
import { OfflineBanner } from '@/shared/components/OfflineBanner';
import { registerServiceWorker } from './sw/register';
import './styles/globals.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000,
      gcTime: 10 * 60 * 1000,
      retry: 1,
      refetchOnWindowFocus: false,
      refetchOnReconnect: true,
    },
  },
});

registerServiceWorker();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <WSProvider>
          <OfflineBanner />
          <RouterProvider router={router} />
          <Toaster />
        </WSProvider>
      </AuthProvider>
      <ReactQueryDevtools initialIsOpen={false} />
    </QueryClientProvider>
  </React.StrictMode>
);
