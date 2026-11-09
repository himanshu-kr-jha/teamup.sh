import { createContext, useContext, useEffect, useState, useCallback, ReactNode } from 'react';
import { useAuthStore } from '@/shared/store/authStore';
import { onlineManager, useQueryClient } from '@tanstack/react-query';
import { api } from '@/shared/api/client';
import { endLocalSession, isSignedOut } from './session';

interface User {
  id: string;
  name: string;
  email: string;
  avatarUrl?: string;
  roles: string[];
  orgId: string;
  mustChangePassword?: boolean;
  /** When they finished or skipped the welcome tour; null until then, undefined until known. */
  tourCompletedAt?: string | null;
  /** What the person's roles let them do; see shared/permissions.ts. */
  permissions?: string[];
}

interface AuthContextType {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  login: (provider: string) => void;
  logout: () => Promise<void>;
  refreshUser: () => Promise<void>;
}

/** The service worker's cache of API reads, named in vite.config.ts. */
const OFFLINE_API_CACHE = 'api-get';

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const { user, isAuthenticated, setUser, logout: logoutStore } = useAuthStore();
  const queryClient = useQueryClient();
  const [isLoading, setIsLoading] = useState(true);

  const refreshUser = useCallback(async () => {
    try {
      // /auth/me answers in the same envelope login does.
      const res = await api.get('/auth/me');
      setUser({ ...res.data.user, permissions: res.data.permissions ?? [] });
    } catch (err) {
      // Anything but a real 401 keeps the persisted user: a failed request at
      // load is not a sign-out, and clearing them closed the websocket for good.
      if (isSignedOut(err)) setUser(null);
    } finally {
      setIsLoading(false);
    }
  }, [setUser]);

  useEffect(() => {
    refreshUser();
  }, [refreshUser]);

  const login = (provider: string) => {
    window.location.href = `/api/auth/login/${provider}`;
  };

  const logout = async () => {
    // Clearing the cache below makes every mounted query fetch again, and the
    // session is gone by then. Those would all come back 401 on the way out.
    onlineManager.setOnline(false);
    try {
      await api.post('/auth/logout');
    } finally {
      await endLocalSession({
        clearStore: logoutStore,
        clearQueries: () => queryClient.clear(),
        clearOfflineCache: async () => window.caches?.delete(OFFLINE_API_CACHE),
        goToLogin: () => window.location.assign('/login'),
      });
    }
  };

  return (
    <AuthContext.Provider value={{ user, isAuthenticated, isLoading, login, logout, refreshUser }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
