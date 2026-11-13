import { createContext, useContext, useEffect, useState, useCallback, ReactNode } from 'react';
import { useAuthStore } from '@/shared/store/authStore';
import { api } from '@/shared/api/client';
import { isSignedOut, signOutLocally } from './session';

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
  /** The newest What's new entry they have seen; null for none yet, undefined until known. */
  whatsNewSeen?: string | null;
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

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const { user, isAuthenticated, setUser } = useAuthStore();
  const [isLoading, setIsLoading] = useState(true);

  const refreshUser = useCallback(async () => {
    try {
      // /auth/me answers in the same envelope login does.
      const res = await api.get('/auth/me');
      setUser({ ...res.data.user, permissions: res.data.permissions ?? [] });
    } catch (err) {
      // Anything but a real 401 keeps the persisted user: a failed request at
      // load is not a sign-out, and clearing them closed the websocket for good.
      // A real one leaves nothing of theirs behind, as signing out does. Only
      // for somebody who was signed in, though: a guest on a meeting link gets
      // a 401 here too, and clearing their page's data started it over.
      if (isSignedOut(err) && useAuthStore.getState().isAuthenticated) await signOutLocally();
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
    try {
      await api.post('/auth/logout');
    } finally {
      await signOutLocally('/login');
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
