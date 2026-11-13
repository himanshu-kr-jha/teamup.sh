import { QueryClient } from '@tanstack/react-query';

/**
 * The one query cache of the app. A module of its own rather than made in
 * main.tsx, so that signing out from outside React (a refresh the server
 * refused, say) can empty it too.
 */
export const queryClient = new QueryClient({
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
