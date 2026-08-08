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
  listSessions: () => api.get('/auth/sessions'),
  revokeSession: (sessionId: string) => api.delete(`/auth/sessions/${sessionId}`),
};

export const channelsApi = {
  list: (params?: { orgId?: string }) => api.get('/channels', { params }),
  create: (data: { name?: string; type: string; description?: string; teamId?: string }) => api.post('/channels', data),
  get: (id: string) => api.get(`/channels/${id}`),
  update: (id: string, data: Partial<{ name: string; description: string; isPrivate: boolean }>) => api.patch(`/channels/${id}`, data),
  delete: (id: string) => api.delete(`/channels/${id}`),
  addMember: (id: string, userId: string) => api.post(`/channels/${id}/members`, { userId }),
  removeMember: (id: string, userId: string) => api.delete(`/channels/${id}/members/${userId}`),
  getMessages: (id: string, params?: { limit?: number; before?: string; after?: string }) => api.get(`/channels/${id}/messages`, { params }),
  sendMessage: (id: string, data: { content: string; tempId: string; replyTo?: string; attachments?: any[] }) => api.post(`/channels/${id}/messages`, data),
  editMessage: (messageId: string, content: string) => api.patch(`/messages/${messageId}`, { content }),
  deleteMessage: (messageId: string) => api.delete(`/messages/${messageId}`),
  addReaction: (messageId: string, emoji: string) => api.post(`/messages/${messageId}/reactions`, { emoji }),
  removeReaction: (messageId: string, emoji: string) => api.delete(`/messages/${messageId}/reactions/${emoji}`),
};

export const messagesApi = {
  search: (params: { q: string; channelIds?: string[]; userIds?: string[]; dateFrom?: string; dateTo?: string; limit?: number; offset?: number }) =>
    api.get('/search/messages', { params }),
};

export const tasksApi = {
  list: (params?: { projectId?: string; assigneeId?: string; status?: string; labelIds?: string[] }) => api.get('/tasks', { params }),
  create: (data: { title: string; description?: string; projectId?: string; assigneeId?: string; priority?: string; taskType?: string; dueDate?: string; labels?: string[] }) => api.post('/tasks', data),
  get: (id: string) => api.get(`/tasks/${id}`),
  update: (id: string, data: Partial<{ title: string; description: string; status: string; priority: string; assigneeId: string; dueDate: string; storyPoints: number }>) => api.patch(`/tasks/${id}`, data),
  delete: (id: string) => api.delete(`/tasks/${id}`),
  addComment: (id: string, content: string) => api.post(`/tasks/${id}/comments`, { content }),
  addLabel: (id: string, labelId: string) => api.post(`/tasks/${id}/labels`, { labelId }),
  removeLabel: (id: string, labelId: string) => api.delete(`/tasks/${id}/labels/${labelId}`),
  addWatcher: (id: string, userId: string) => api.post(`/tasks/${id}/watchers`, { userId }),
  removeWatcher: (id: string, userId: string) => api.delete(`/tasks/${id}/watchers/${userId}`),
  getActivities: (id: string) => api.get(`/tasks/${id}/activities`),
};

export const callsApi = {
  list: (params?: { status?: string; channelId?: string }) => api.get('/calls', { params }),
  create: (data: { title?: string; channelId?: string; callType: 'video' | 'audio'; invitees?: string[] }) => api.post('/calls', data),
  get: (id: string) => api.get(`/calls/${id}`),
  getToken: (id: string) => api.post(`/calls/${id}/token`),
  startRecording: (id: string) => api.post(`/calls/${id}/record`),
  end: (id: string) => api.post(`/calls/${id}/end`),
};

export const filesApi = {
  getUploadUrl: (data: { category: string; refId: string; filename: string; contentType: string; size: number }) => api.post('/files/upload-url', data),
  initiateMultipart: (data: { category: string; refId: string; filename: string; contentType: string; size: number }) => api.post('/files/multipart/initiate', data),
  completeMultipart: (data: { objectKey: string; uploadId: string; parts: { partNumber: number; etag: string }[] }) => api.post('/files/multipart/complete', data),
  getDownloadUrl: (objectKey: string, filename?: string, inline?: boolean) => api.get('/files/download-url', { params: { objectKey, filename, inline } }),
};

export const notificationsApi = {
  list: (params?: { unreadOnly?: boolean; limit?: number; offset?: number }) => api.get('/notifications', { params }),
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
