import { create } from 'zustand';

interface UIState {
  sidebarOpen: boolean;
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  activeChannelId: string | null;
  setActiveChannelId: (id: string | null) => void;
}

export const useUIStore = create<UIState>((set) => ({
  sidebarOpen: true,
  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
  setSidebarOpen: (open) => set({ sidebarOpen: open }),
  activeChannelId: null,
  setActiveChannelId: (id) => set({ activeChannelId: id }),
}));

interface Notification {
  id: string;
  type: string;
  title: string;
  body: string;
  data?: Record<string, any>;
  readAt?: string;
  createdAt: string;
}

interface NotificationState {
  notifications: Notification[];
  unreadCount: number;
  addNotification: (notification: Notification) => void;
  markRead: (id: string) => void;
  markAllRead: () => void;
  setNotifications: (notifications: Notification[]) => void;
}

export const useNotificationStore = create<NotificationState>((set) => ({
  notifications: [],
  unreadCount: 0,
  addNotification: (notification) => set((state) => ({
    notifications: [notification, ...state.notifications],
    unreadCount: state.unreadCount + 1,
  })),
  markRead: (id) => set((state) => ({
    notifications: state.notifications.map((n) => n.id === id ? { ...n, readAt: new Date().toISOString() } : n),
    unreadCount: Math.max(0, state.unreadCount - 1),
  })),
  markAllRead: () => set((state) => ({
    notifications: state.notifications.map((n) => ({ ...n, readAt: new Date().toISOString() })),
    unreadCount: 0,
  })),
  setNotifications: (notifications) => set({ notifications, unreadCount: notifications.filter((n) => !n.readAt).length }),
}));
