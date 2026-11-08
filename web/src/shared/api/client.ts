import axios, { AxiosError, InternalAxiosRequestConfig } from 'axios';
import type {
  ContributionDetail,
  Contributions,
  Dashboard,
  Scrum,
  ScrumSummary,
} from '@/shared/types/dashboard';
import { loginPathFor } from '@/features/auth/nextPath';
import type { AvailabilityPerson, CalendarItem, CalendarKind } from '@/features/calendar/layout';
import type { CallLog } from '@/features/calls/callLog';
import type { AttendanceDay, StandingForm, StandingMeeting } from '@/features/calls/standing/standing';

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
        const publicPaths = ['/login', '/wizard', '/status'];
        const isPublicPath = publicPaths.some(p => window.location.pathname === p || window.location.pathname.startsWith(p + '/'));
        if (!isPublicPath) {
          window.location.href = loginPathFor(window.location.pathname, window.location.search);
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

export interface MailSettings {
  host: string;
  port: number;
  user: string;
  from: string;
  passwordSet: boolean;
  configured: boolean;
}

/** The organization's look, readable signed in or not. Empty means TeamUp's own. */
export interface Branding {
  name: string;
  color: string;
  logoUrl: string;
}

export const brandingApi = {
  get: () => api.get<Branding>('/branding'),
};

export type ServiceState = 'operational' | 'outage';

export interface StatusReport {
  /** Everything working, something not, or nothing that matters. */
  overall: 'operational' | 'degraded' | 'outage';
  checkedAt: string;
  historyAvailable: boolean;
  components: Array<{
    key: string;
    name: string;
    description: string;
    status: ServiceState;
    uptime?: number;
    hours: Array<{ start: string; status: 'up' | 'partial' | 'down' | 'none'; downMinutes: number }>;
    incidents: Array<{ start: string; end?: string; minutes: number }>;
  }>;
}

export const statusApi = {
  get: () => api.get<StatusReport>('/status'),
};

export const orgApi = {
  get: () => api.get('/org'),
  update: (data: { name?: string; logoUrl?: string }) => api.patch('/org', data),
  listUsers: () => getList('/users', 'users'),
  addMember: (data: { name: string; email: string; role?: string }) =>
    api.post<{
      user: { id: string; name: string; email: string };
      temporaryPassword: string;
      /** Whether the credentials were posted to them, or need passing on by hand. */
      emailed: boolean;
      /** Why not: "not_configured", or what the mail server said. */
      mailProblem?: string;
    }>('/users', data),
  /**
   * Create a whole CSV of colleagues at once. Every row is reported back
   * separately - one bad address does not cost the rest of the batch - and the
   * generated passwords come back with them, which is the only time they are
   * ever visible.
   */
  importMembers: (csv: string) =>
    api.post<{
      created: number;
      failed: number;
      outcomes: Array<{
        row: number;
        name: string;
        email: string;
        status: 'created' | 'duplicate' | 'invalid' | 'failed';
        message?: string;
        temporaryPassword?: string;
        emailed?: boolean;
      }>;
      /** Why the rest were not emailed, once one failed. */
      mailProblem?: string;
    }>('/users/import', { csv }),
  /** The mail server new members' details go through. The password never comes back. */
  getMail: () => api.get<MailSettings>('/org/mail'),
  saveMail: (data: { host: string; port: number; user: string; password: string; from: string }) =>
    api.put<MailSettings>('/org/mail', data),
  /** Sends a test message to whoever asks, with the saved settings. */
  testMail: () => api.post<{ sent: boolean; sentTo: string; problem?: string }>('/org/mail/test'),
  /** The colour and logo. A logo left out is kept; an empty one removes it. */
  saveBranding: (data: { color: string; logo?: string }) => api.put<Branding>('/org/branding', data),
  getUser: (id: string) => api.get(`/users/${id}`),
  updateUser: (id: string, data: { name?: string; avatarUrl?: string }) => api.patch(`/users/${id}`, data),
  deactivateUser: (id: string) => api.delete(`/users/${id}`),
  /** Ends their sessions; the password in the response is never visible again. */
  resetPassword: (id: string) => api.post<{ temporaryPassword: string }>(`/users/${id}/reset-password`),
  listRoles: () => getList('/roles', 'roles'),
  createRole: (data: { name: string; description?: string; permissions?: string[] }) => api.post('/roles', data),
  updateRole: (id: string, data: { name?: string; description?: string; permissions?: string[] }) =>
    api.patch(`/roles/${id}`, data),
  deleteRole: (id: string) => api.delete(`/roles/${id}`),
  assignRole: (userId: string, roleId: string) => api.post(`/users/${userId}/roles`, { roleId }),
  removeRole: (userId: string, roleId: string) => api.delete(`/users/${userId}/roles/${roleId}`),
  listTeams: () => getList('/teams', 'teams'),
  createTeam: (data: { name: string; description?: string; managerId?: string; parentId?: string }) =>
    api.post('/teams', data),
  // parentId: omit to leave the team where it is in the tree, or pass an empty
  // string to lift it to the top level. Without the distinction a team could
  // never be moved back out from under a parent.
  updateTeam: (
    id: string,
    data: { name?: string; description?: string; managerId?: string; parentId?: string },
  ) => api.patch(`/teams/${id}`, data),
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
  /**
   * One thread: a message and the replies to it. Any message in the thread
   * identifies it, so this works just as well on a reply as on the root.
   */
  thread: (messageId: string) =>
    api.get<{ channelId: string; root: any; replies: any[] }>(`/messages/${messageId}/thread`),
  /** Who has read a particular message. */
  messageReads: (messageId: string) =>
    getList<{ userId: string; name: string; readAt: string }>(`/messages/${messageId}/reads`, 'reads'),
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
  /** Files shared in channels the caller can read, matched on name. */
  searchFiles: (params: { q: string; channelId?: string; limit?: number; offset?: number }) =>
    getList<{
      id: string;
      filename: string;
      contentType: string;
      sizeBytes: number;
      objectKey: string;
      channelId: string;
      channelName: string;
      uploaderName: string;
      createdAt: string;
    }>('/search/files', 'results', params),
};

export const presenceApi = {
  listOnline: () => getList('/presence/org/online', 'users'),
  /** Everybody in the organization, whether or not they are connected. */
  listAvailability: () =>
    getList<{
      userId: string;
      name: string;
      status: string;
      note?: string;
      until?: string;
    }>('/presence/availability', 'people'),
  /** What the caller has chosen to say about themselves. */
  setAvailability: (data: { status: string; note?: string; until?: string | null }) =>
    api.put('/presence/availability', data),
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
  /** The people this caller may actually assign work to, per the org chart. */
  assignableUsers: () =>
    getList<{ userId: string; name: string; email: string }>('/tasks/assignable-users', 'users'),
  managedTeams: () =>
    getList<{ id: string; name: string; members: { userId: string; name: string; email: string }[] }>(
      '/tasks/managed-teams',
      'teams'
    ),
  create: (data: { title: string; description?: string; projectId?: string; assigneeId?: string; priority?: string; taskType?: string; dueDate?: string; labels?: string[] }) => api.post('/tasks', data),
  get: (id: string) => api.get(`/tasks/${id}`),
  update: (id: string, data: Partial<{ title: string; description: string; status: string; priority: string; assigneeId: string; dueDate: string; storyPoints: number }>) => api.patch(`/tasks/${id}`, data),
  delete: (id: string) => api.delete(`/tasks/${id}`),
  addComment: (id: string, content: string, attachmentIds?: string[]) =>
    api.post(`/tasks/${id}/comments`, { content, attachmentIds }),
  addLabel: (id: string, labelId: string) => api.post(`/tasks/${id}/labels`, { labelId }),
  removeLabel: (id: string, labelId: string) => api.delete(`/tasks/${id}/labels/${labelId}`),
  listWatchers: (id: string) => getList(`/tasks/${id}/watchers`, 'users'),
  addWatcher: (id: string, userId: string) => api.post(`/tasks/${id}/watchers`, { userId }),
  removeWatcher: (id: string, userId: string) => api.delete(`/tasks/${id}/watchers/${userId}`),
  getActivities: (id: string) => getList(`/tasks/${id}/activities`, 'activities'),
  listComments: (id: string) => getList(`/tasks/${id}/comments`, 'comments'),
  listLabels: () => getList('/labels', 'labels'),
  createLabel: (data: { name: string; color?: string }) => api.post('/labels', data),
  /** The caller's dashboard. The server decides how wide it is, by who is asking. */
  dashboard: (teamId?: string) => api.get<Dashboard>('/tasks/dashboard', { params: { teamId } }),
  contributions: (params: { from: string; to: string; teamId?: string }) =>
    api.get<Contributions>('/tasks/contributions', { params }),
  contributionDetail: (userId: string, params: { from: string; to: string }) =>
    api.get<ContributionDetail>(`/tasks/contributions/${userId}`, { params }),
};

export const scrumsApi = {
  /** Saves the snapshot. It can never be edited afterwards. */
  complete: (data: { teamId?: string; notes?: string }) => api.post<Scrum>('/scrums', data),
  get: (id: string) => api.get<Scrum>(`/scrums/${id}`),
  /** Newest first. `before` is the last item's createdAt. */
  list: (params: { teamId?: string; limit?: number; before?: string }) =>
    getList<ScrumSummary>('/scrums', 'scrums', params),
};

export const callsApi = {
  list: (params?: { status?: string; channelId?: string }) => getList('/calls', 'calls', params),
  create: (data: { title?: string; channelId?: string; callType: 'video' | 'audio'; invitees?: string[] }) =>
    api.post<{ id: string; roomName: string }>('/calls', data),
  get: (id: string) => api.get(`/calls/${id}`),
  getToken: (id: string) => api.post(`/calls/${id}/token`),
  startRecording: (id: string) => api.post(`/calls/${id}/record`),
  end: (id: string) => api.post(`/calls/${id}/end`),
  /** Quietens somebody's microphone. Only the media server can do this, and
   *  it is one way - nobody turns another person's microphone back on. */
  muteParticipant: (callId: string, identity: string) =>
    api.post(`/calls/${callId}/mute/${identity}`),
  /** Turns a call down and tells whoever started it why. */
  decline: (id: string, reason: 'declined' | 'no_answer' = 'declined') =>
    api.post(`/calls/${id}/decline`, { reason }),
  /** Asks to join a private call. A repeat while one is waiting is the same request. */
  knock: (id: string) => api.post<{ request: KnockRequest }>(`/calls/${id}/knock`),
  /** Who is waiting or has waited. Members only. */
  requests: (id: string) => getList<KnockRequest>(`/calls/${id}/requests`, 'requests'),
  admit: (id: string, requestId: string) => api.post(`/calls/${id}/requests/${requestId}/admit`),
  deny: (id: string, requestId: string) => api.post(`/calls/${id}/requests/${requestId}/deny`),
  /** The audit trail. Anybody who may not read it gets 404. */
  log: (id: string) => api.get<CallLog>(`/calls/${id}/log`),
};

export interface KnockRequest {
  id: string;
  userId: string;
  userName: string;
  status: 'pending' | 'admitted' | 'denied' | 'ended';
  createdAt: string;
}

export interface BlockBody {
  kind: Exclude<CalendarKind, 'busy'>;
  title?: string;
  startsAt: string;
  endsAt: string;
}

export interface MeetingBody {
  title: string;
  startsAt: string;
  endsAt: string;
  inviteeIds: string[];
}

export interface MeetingConflict {
  userId: string;
  kind: 'focus' | 'meeting';
}

export const calendarApi = {
  /** One person's calendar, at the level the caller may see. */
  get: (params: { userId?: string; from: string; to: string }) =>
    getList<CalendarItem>('/calendar', 'items', params),
  /** Whether people are free. Never carries a title. */
  availability: (params: { userIds: string; from: string; to: string }) =>
    getList<AvailabilityPerson>('/calendar/availability', 'people', params),
  createBlock: (data: BlockBody) => api.post('/calendar/blocks', data),
  updateBlock: (id: string, data: Partial<BlockBody>) => api.patch(`/calendar/blocks/${id}`, data),
  deleteBlock: (id: string) => api.delete(`/calendar/blocks/${id}`),
};

export const meetingsApi = {
  create: (data: MeetingBody) =>
    api.post<{ meeting: { id: string }; conflicts: MeetingConflict[] | null }>('/meetings', data),
  update: (id: string, data: Partial<MeetingBody>) =>
    api.patch<{ meeting: { id: string }; conflicts: MeetingConflict[] | null }>(`/meetings/${id}`, data),
  cancel: (id: string) => api.delete(`/meetings/${id}`),
};

export const standingApi = {
  list: () => getList<StandingMeeting>('/standing-meetings', 'standingMeetings'),
  create: (data: StandingForm) => api.post<{ standingMeeting: StandingMeeting }>('/standing-meetings', data),
  update: (id: string, data: Partial<StandingForm>) =>
    api.patch<{ standingMeeting: StandingMeeting }>(`/standing-meetings/${id}`, data),
  /** Archives it: the room goes and its history stays. */
  archive: (id: string) => api.delete(`/standing-meetings/${id}`),
  /** Opens today's occurrence. */
  join: (id: string) =>
    api.post<{ callId: string; roomName: string; token: string; date: string }>(`/standing-meetings/${id}/join`),
  /** Scope is decided by the server, by who is asking. */
  attendance: (id: string, params: { from: string; to: string; teamId?: string }) =>
    getList<AttendanceDay>(`/standing-meetings/${id}/attendance`, 'days', params),
};

/** An upload in progress, as the server describes it. */
export interface MultipartUpload {
  id: string;
  objectKey: string;
  filename: string;
  contentType: string;
  totalBytes: number;
  /** Every part but the last carries exactly this many bytes. */
  partSize: number;
  partCount: number;
  createdAt: string;
}

/** A part object storage already holds. */
export interface UploadedPart {
  partNumber: number;
  size: number;
  etag: string;
}

/** One presigned part upload. The length is signed in, so the slice must match it exactly. */
export interface PartUpload {
  partNumber: number;
  url: string;
  contentLength: number;
  expiresAt: string;
}

export const filesApi = {
  getUploadUrl: (data: { category: string; refId: string; filename: string; contentType: string; size: number }) => api.post('/files/upload-url', data),
  // Registers a finished upload; without this the file has no record.
  record: (data: { category: string; refId: string; objectKey: string; filename: string; contentType: string; size: number }) =>
    api.post<{ id: string; filename: string; contentType: string; sizeBytes: number; objectKey: string }>(
      '/files/record',
      data
    ),
  list: (category: string, refId: string) => getList('/files', 'attachments', { category, refId }),
  /** Throws away an upload that was staged and then removed before sending. */
  discard: (id: string) => api.delete(`/files/${id}`),
  /**
   * What the server will accept, and where it switches upload paths. The
   * client has to do the same arithmetic the server does, or a file lands in
   * the gap between them.
   */
  limits: () =>
    api.get<{ maxUploadBytes: number; multipartThresholdBytes: number }>('/files/limits'),

  // ── Resumable (multipart) uploads ───────────────────────────────────────
  //
  // Big files go up in parts so that a connection that dies at 80% resumes
  // instead of restarting. The server holds the plan; the browser holds only
  // the file, and asks what is still missing every time it comes back.

  /** Starts a resumable upload and returns its plan. */
  initiateMultipart: (data: {
    category: string;
    refId: string;
    filename: string;
    contentType: string;
    size: number;
  }) => api.post<MultipartUpload>('/files/multipart', data),

  /** The caller's own unfinished uploads for one parent, offered back after a reload. */
  listMultipartUploads: (category: string, refId: string) =>
    getList<MultipartUpload>('/files/multipart', 'uploads', { category, refId }),

  /** The plan plus which parts object storage already holds. */
  multipartStatus: (id: string) =>
    api.get<{ upload: MultipartUpload; uploaded: UploadedPart[] }>(`/files/multipart/${id}`),

  /**
   * Presigned URLs for parts. Omit partNumbers to be given whatever is still
   * missing - which is all a resuming browser knows how to ask for.
   */
  signParts: (id: string, partNumbers?: number[]) =>
    api.post<{ parts: PartUpload[] }>(`/files/multipart/${id}/parts`, { partNumbers: partNumbers ?? [] }),

  /** Assembles the object and records the attachment. Sends no part list: the server reads them from storage. */
  completeMultipart: (id: string) =>
    api.post<{ id: string; filename: string; contentType: string; sizeBytes: number; objectKey: string }>(
      `/files/multipart/${id}/complete`
    ),

  /** Gives up and reclaims the parts now rather than waiting for the hourly sweep. */
  abortMultipart: (id: string) => api.delete(`/files/multipart/${id}`),
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
