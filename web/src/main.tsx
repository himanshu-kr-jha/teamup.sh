import React from 'react';
import ReactDOM from 'react-dom/client';
import { RouterProvider } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { ReactQueryDevtools } from '@tanstack/react-query-devtools';
import { router } from './app/router';
import { AuthProvider } from '@/features/auth/AuthProvider';
import { WSProvider } from '@/features/websocket/WSProvider';
import { Toaster } from '@/shared/components/ui/toaster';
import { OfflineBanner } from '@/shared/components/OfflineBanner';
import { LoadingScreen } from '@/shared/components/LoadingScreen';
import { BrandingSync } from '@/features/branding/useBranding';
import { queryClient } from '@/shared/api/queryClient';
import { registerServiceWorker } from './sw/register';
import { watchInstall } from '@/features/install/install';
import './styles/globals.css';

registerServiceWorker();
watchInstall();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <WSProvider>
          <BrandingSync />
          <OfflineBanner />
          {/* The first loaders ask the server things, and until they answer the router
              draws this: the same screen index.html shows while the code downloads. */}
          <RouterProvider router={router} fallbackElement={<LoadingScreen />} />
          <Toaster />
        </WSProvider>
      </AuthProvider>
      <ReactQueryDevtools initialIsOpen={false} />
    </QueryClientProvider>
  </React.StrictMode>
);
