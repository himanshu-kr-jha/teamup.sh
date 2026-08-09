import axios, { AxiosError, InternalAxiosRequestConfig } from 'axios';

export const api = axios.create({
  baseURL: '/api',
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Request interceptor: add CSRF token if needed
api.interceptors.request.use((config: InternalAxiosRequestConfig) => {
  const csrf = document.querySelector('meta[name="csrf-token"]')?.getAttribute('content');
  if (csrf) {
    config.headers.set('X-CSRF-Token', csrf);
  }
  return config;
});

// Response interceptor: handle 401 -> try refresh
let isRefreshing = false;
let failedQueue: Array<{
  resolve: (value: any) => void;
  reject: (reason: any) => void;
}> = [];

const processQueue = (error: any, token: string | null = null) => {
  failedQueue.forEach((prom) => {
    if (error) {
      prom.reject(error);
    } else {
      prom.resolve(token);
    }
  });
  failedQueue = [];
};

api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean };

    if (error.response?.status === 401 && !originalRequest._retry && !originalRequest.url?.includes('/auth/refresh')) {
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          failedQueue.push({ resolve, reject });
        })
          .then(() => api.request(originalRequest))
          .catch((err) => Promise.reject(err));
      }

      originalRequest._retry = true;
      isRefreshing = true;

      try {
        await api.post('/auth/refresh');
        processQueue(null);
        return api.request(originalRequest);
      } catch (err) {
        processQueue(err, null);
        // Redirect to login, but not if already on login or wizard pages
        const publicPaths = ['/login', '/wizard'];
        const isPublicPath = publicPaths.some(p => window.location.pathname === p || window.location.pathname.startsWith(p + '/'));
        if (!isPublicPath) {
          window.location.href = '/login';
        }
        return Promise.reject(err);
      } finally {
        isRefreshing = false;
      }
    }

    return Promise.reject(error);
  }
);

// API helper functions
export const authApi = {
  me: () => api.get('/auth/me'),
  logout: () => api.post('/auth/logout'),
  linkAccount: (provider: string) => api.post(`/auth/link/${provider}`),
  unlinkAccount: (provider: string) => api.delete(`/auth/link/${provider}`),
  listSessions: () => getList('/auth/sessions', 'sessions'),
  revokeSession: (sessionId: string) => api.delete(`/auth/sessions/${sessionId}`),
  changePassword: (data: { currentPassword: string; newPassword: string }) =>
    api.post('/auth/password', data),
};

export const orgApi = {
  get: () => api.get('/org'),
  update: (data: { name?: string; logoUrl?: string }) => api.patch('/org', data),
  listUsers: () => getList('/users', 'users'),
  addMember: (data: { name: string; email: string; role?: string }) =>
    api.post<{ user: { id: string; name: string; email: string }; temporaryPassword: string }>(
      '/users',
      data
    ),
  getUser: (id: string) => api.get(`/users/${id}`),
  updateUser: (id: string, data: { name?: string; avatarUrl?: string }) => api.patch(`/users/${id}`, data),
  deactivateUser: (id: string) => api.delete(`/users/${id}`),
  listRoles: () => getList('/roles', 'roles'),
  createRole: (data: { name: string; description?: string; permissions?: string[] }) => api.post('/roles', data),
  updateRole: (id: string, data: { name?: string; description?: string; permissions?: string[] }) =>
    api.patch(`/roles/${id}`, data),
  deleteRole: (id: string) => api.delete(`/roles/${id}`),
  assignRole: (userId: string, roleId: string) => api.post(`/users/${userId}/roles`, { roleId }),
  removeRole: (userId: string, roleId: string) => api.delete(`/users/${userId}/roles/${roleId}`),
  listTeams: () => getList('/teams', 'teams'),
  createTeam: (data: { name: string; description?: string; managerId?: string }) => api.post('/teams', data),
  updateTeam: (id: string, data: { name?: string; description?: string; managerId?: string }) =>
    api.patch(`/teams/${id}`, data),
  deleteTeam: (id: string) => api.delete(`/teams/${id}`),
  listTeamMembers: (id: string) => getList(`/teams/${id}/members`, 'users'),
  addTeamMember: (id: string, userId: string) => api.post(`/teams/${id}/members`, { userId }),
  removeTeamMember: (id: string, userId: string) => api.delete(`/teams/${id}/members/${userId}`),
};

// The API wraps collections in a named key ({"channels": [...]}) while the
// pages expect response.data to be the array itself, so an unwrapped response
// made every list render crash with "x.map is not a function". getList
// normalises that in one place and tolerates a null collection.
async function getList<T = any>(url: string, key: string, params?: unknown) {
  const res = await api.get(url, { params: params as any });
  const value = res.data?.[key];
  return { ...res, data: (Array.isArray(value) ? value : []) as T[] };
}

export const channelsApi = {
  list: (params?: { orgId?: string }) => getList('/channels', 'channels', params),
  create: (data: { name?: string; type: string; description?: string; teamId?: string }) => api.post('/channels', data),
  /** Opens the one-to-one conversation with somebody, creating it if needed. */
  openDirect: (userId: string) => api.post<{ id: string; name?: string; type: string }>('/channels/direct', { userId }),
  get: (id: string) => api.get(`/channels/${id}`),
  update: (id: string, data: Partial<{ name: string; description: string; isPrivate: boolean }>) => api.patch(`/channels/${id}`, data),
  delete: (id: string) => api.delete(`/channels/${id}`),
  listMembers: (id: string) => getList(`/channels/${id}/members`, 'members'),
  addMember: (id: string, userId: string, role?: string) => api.post(`/channels/${id}/members`, { userId, role }),
  removeMember: (id: string, userId: string) => api.delete(`/channels/${id}/members/${userId}`),
  markRead: (id: string, messageId: string) => api.post(`/channels/${id}/read`, { messageId }),
  getMessages: (id: string, params?: { limit?: number; before?: string; after?: string }) => getList(`/channels/${id}/messages`, 'messages', params),
  sendMessage: (id: string, data: { content: string; tempId: string; replyTo?: string; attachmentIds?: string[] }) =>
    api.post(`/channels/${id}/messages`, data),
  editMessage: (messageId: string, content: string) => api.patch(`/messages/${messageId}`, { content }),
  deleteMessage: (messageId: string) => api.delete(`/messages/${messageId}`),
  addReaction: (messageId: string, emoji: string) => api.post(`/messages/${messageId}/reactions`, { emoji }),
  removeReaction: (messageId: string, emoji: string) => api.delete(`/messages/${messageId}/reactions/${emoji}`),
};

export const messagesApi = {
  search: (params: { q: string; channelId?: string; userId?: string; dateFrom?: string; dateTo?: string; limit?: number; offset?: number }) =>
    getList('/search/messages', 'results', params),
};

export const presenceApi = {
  listOnline: () => getList('/presence/org/online', 'users'),
  get: (userId: string) => api.get(`/presence/${userId}`),
  update: (status: string) => api.patch('/presence', { status }),
};

export const tasksApi = {
  list: (params?: {
    projectId?: string;
    assigneeId?: string;
    status?: string;
    labelIds?: string[];
    /** 'mine' is the caller's own work; 'team' is their reports'. */
    scope?: 'mine' | 'team';
    teamId?: string;
  }) => getList('/tasks', 'tasks', params),
  managedTeams: () =>
    getList<{ id: string; name: string; members: { userId: string; name: string; email: string }[] }>(
      '/tasks/managed-teams',
      'teams'
    ),
  create: (data: { title: string; description?: string; projectId?: string; assigneeId?: string; priority?: string; taskType?: string; dueDate?: string; labels?: string[] }) => api.post('/tasks', data),
  get: (id: string) => api.get(`/tasks/${id}`),
  update: (id: string, data: Partial<{ title: string; description: string; status: string; priority: string; assigneeId: string; dueDate: string; storyPoints: number }>) => api.patch(`/tasks/${id}`, data),
  delete: (id: string) => api.delete(`/tasks/${id}`),
  addComment: (id: string, content: string) => api.post(`/tasks/${id}/comments`, { content }),
  addLabel: (id: string, labelId: string) => api.post(`/tasks/${id}/labels`, { labelId }),
  removeLabel: (id: string, labelId: string) => api.delete(`/tasks/${id}/labels/${labelId}`),
  listWatchers: (id: string) => getList(`/tasks/${id}/watchers`, 'users'),
  addWatcher: (id: string, userId: string) => api.post(`/tasks/${id}/watchers`, { userId }),
  removeWatcher: (id: string, userId: string) => api.delete(`/tasks/${id}/watchers/${userId}`),
  getActivities: (id: string) => getList(`/tasks/${id}/activities`, 'activities'),
  listComments: (id: string) => getList(`/tasks/${id}/comments`, 'comments'),
  listLabels: () => getList('/labels', 'labels'),
  createLabel: (data: { name: string; color?: string }) => api.post('/labels', data),
};

export const callsApi = {
  list: (params?: { status?: string; channelId?: string }) => getList('/calls', 'calls', params),
  create: (data: { title?: string; channelId?: string; callType: 'video' | 'audio'; invitees?: string[] }) =>
    api.post<{ id: string; roomName: string }>('/calls', data),
  get: (id: string) => api.get(`/calls/${id}`),
  getToken: (id: string) => api.post(`/calls/${id}/token`),
  startRecording: (id: string) => api.post(`/calls/${id}/record`),
  end: (id: string) => api.post(`/calls/${id}/end`),
};

export const filesApi = {
  getUploadUrl: (data: { category: string; refId: string; filename: string; contentType: string; size: number }) => api.post('/files/upload-url', data),
  // Registers a finished upload; without this the file has no record.
  record: (data: { category: string; refId: string; objectKey: string; filename: string; contentType: string; size: number }) =>
    api.post<{ id: string; filename: string; contentType: string; sizeBytes: number; objectKey: string }>(
      '/files/record',
      data
    ),
  list: (category: string, refId: string) => getList('/files', 'attachments', { category, refId }),
  initiateMultipart: (data: { category: string; refId: string; filename: string; contentType: string; size: number }) => api.post('/files/multipart/initiate', data),
  completeMultipart: (data: { objectKey: string; uploadId: string; parts: { partNumber: number; etag: string }[] }) => api.post('/files/multipart/complete', data),
  getDownloadUrl: (objectKey: string, filename?: string, inline?: boolean) => api.get('/files/download-url', { params: { objectKey, filename, inline } }),
};

export const notificationsApi = {
  unreadCount: () => api.get('/notifications/unread-count'),
  vapidKey: () => api.get('/notifications/vapid-key'),
  list: (params?: { unreadOnly?: boolean; limit?: number; offset?: number }) => getList('/notifications', 'notifications', params),
  markRead: (id: string) => api.patch(`/notifications/${id}/read`),
  markAllRead: () => api.patch('/notifications/read-all'),
  archive: (id: string) => api.delete(`/notifications/${id}`),
  subscribe: (data: { endpoint: string; p256dh: string; auth: string; deviceName?: string }) => api.post('/notifications/subscribe', data),
  unsubscribe: (endpoint: string) => api.delete('/notifications/subscribe', { data: { endpoint } }),
};

export const wizardApi = {
  status: () => api.get('/wizard/status'),
  step1: (data: { orgName: string; orgSubdomain: string; orgLogo?: string }) => api.post('/wizard/step1', data),
  step2: (data: { adminName: string; adminEmail: string; adminPassword: string }) => api.post('/wizard/step2', data),
  step3: (data: { oidcProvider: string; googleClientId?: string; googleClientSecret?: string; entraClientId?: string; entraClientSecret?: string; entraTenantId?: string; customIssuer?: string; customClientId?: string; customClientSecret?: string }) => api.post('/wizard/step3', data),
  step4: (data: { minioEndpoint: string; minioAccessKey: string; minioSecretKey: string; minioBucket: string; livekitUrl: string; livekitApiKey: string; livekitApiSecret: string; smtpHost?: string; smtpPort?: number; smtpUser?: string; smtpPass?: string; smtpFrom?: string }) => api.post('/wizard/step4', data),
  step5: () => api.post('/wizard/step5'),
  testService: (service: 'minio' | 'livekit' | 'smtp', config: any) => api.post('/wizard/test-service', { service, ...config }),
};
