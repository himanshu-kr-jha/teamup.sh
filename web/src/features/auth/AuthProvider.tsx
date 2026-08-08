import { createContext, useContext, useEffect, useState, useCallback, ReactNode } from 'react';
import { useAuthStore } from '@/shared/store/authStore';
import { api } from '@/shared/api/client';

interface User {
  id: string;
  name: string;
  email: string;
  avatarUrl?: string;
  roles: string[];
  orgId: string;
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
  const { user, isAuthenticated, setUser, logout: logoutStore } = useAuthStore();
  const [isLoading, setIsLoading] = useState(true);

  const refreshUser = useCallback(async () => {
    try {
      const res = await api.get('/auth/me');
      setUser(res.data);
    } catch {
      setUser(null);
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
      logoutStore();
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
