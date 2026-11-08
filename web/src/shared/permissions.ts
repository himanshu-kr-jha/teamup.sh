/**
 * What the signed-in person's roles let them do, as the server lists it
 * (internal/auth/permissions.go). The pages only decide what to offer; the
 * server checks every request again.
 */
export const PERMS = {
  orgSettings: 'org.settings',
  members: 'members.manage',
  roles: 'roles.manage',
  teams: 'teams.manage',
  tasksAll: 'tasks.all',
  channelsAll: 'channels.all',
  meetingsAll: 'meetings.all',
  callsRecord: 'calls.record',
  notesSettings: 'notes.settings',
  notesReadAll: 'notes.read_all',
} as const;

export type PermissionKey = (typeof PERMS)[keyof typeof PERMS];

/** Everything, which only the owner role holds. */
export const EVERY = '*';

/**
 * Whether somebody holds a permission. A user remembered from before the
 * server said what they may do falls back to the built-in roles, until the
 * next check of who is signed in replaces it.
 */
export function can(user: { roles?: string[]; permissions?: string[] } | null | undefined, perm: PermissionKey): boolean {
  if (!user) return false;
  if (!user.permissions) return !!user.roles?.some((r) => r === 'owner' || r === 'admin');
  return user.permissions.includes(EVERY) || user.permissions.includes(perm);
}

/** Whether somebody holding `held` may hand `perm` on: nobody grants what they lack. */
export function grants(held: string[] | undefined, perm: string): boolean {
  return !!held && (held.includes(EVERY) || held.includes(perm));
}
