import { create } from 'zustand';
import { persist } from 'zustand/middleware';

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

interface AuthState {
  user: User | null;
  isAuthenticated: boolean;
  setUser: (user: User | null) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      isAuthenticated: false,
      setUser: (user) => set({ user, isAuthenticated: !!user }),
      logout: () => set({ user: null, isAuthenticated: false }),
    }),
    { name: 'auth-storage', partialize: (s) => ({ user: s.user, isAuthenticated: s.isAuthenticated }) }
  )
);
