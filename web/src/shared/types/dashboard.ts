/**
 * The task dashboard, scrum and contribution API, as the server answers it.
 * Ids and times are strings on the wire.
 */
export type DashboardScope = 'self' | 'team' | 'org';

export interface TeamRef {
  id: string;
  name: string;
}

export interface PersonRef {
  id: string;
  name: string;
}

export interface TaskCounts {
  pending: number;
  ongoing: number;
  blocked: number;
  /** An overdue task is also counted in its status, so never add this to the others. */
  overdue: number;
  completed: number;
  /** Open tasks they are on and not assigned to. Counted nowhere else. */
  addedTo: number;
}

export interface TaskSummary {
  id: string;
  title: string;
  status: string;
  priority: string;
  overdue: boolean;
  dueDate: string | null;
  completedAt: string | null;
  createdAt: string;
  /** What a blocked task is waiting for. Absent unless it is blocked. */
  blockedOn?: BlockedOn;
}

/** What a blocked task is waiting for. An empty note means no reason was given. */
export interface BlockedOn {
  note: string;
  user: PersonRef | null;
  task: { id: string; title: string; key?: string } | null;
}

/** A reason as it is sent when a task is marked blocked or its reason changed. */
export interface BlockedOnInput {
  note: string;
  userId: string | null;
  taskId: string | null;
}

export interface TaskBuckets {
  pending: TaskSummary[];
  ongoing: TaskSummary[];
  blocked: TaskSummary[];
  overdue: TaskSummary[];
  completed: TaskSummary[];
  addedTo: TaskSummary[];
}

export interface DashboardPerson {
  userId: string;
  name: string;
  teams: TeamRef[];
  /** The managers of every team they are on, never themselves. */
  leads: PersonRef[];
  counts: TaskCounts;
  tasks: TaskBuckets;
  newSinceLastScrum: string[];
}

export interface Dashboard {
  scope: DashboardScope;
  /** The team filter's options. Empty for somebody who answers for nobody. */
  teams: TeamRef[];
  totals: TaskCounts;
  lastScrumAt: string | null;
  people: DashboardPerson[];
}

export interface ScrumSummary {
  id: string;
  teamId: string | null;
  teamName: string | null;
  scope: 'team' | 'org';
  heldById: string | null;
  heldByName: string;
  notes: string;
  doneSince: string;
  createdAt: string;
  participantCount: number;
  entryCount: number;
}

export interface ScrumParticipant {
  userId: string;
  name: string;
  counts: TaskCounts;
  /** Their row in the holder's table, from 0. Null or absent on a scrum saved before the table. */
  position?: number | null;
  /** Their leads' names when it was saved. Null or absent on a scrum saved before the table. */
  leads?: string[] | null;
}

export interface ScrumEntry {
  userId: string;
  taskId: string;
  title: string;
  status: string;
  priority: string;
  overdue: boolean;
  dueDate: string | null;
  completedAt: string | null;
  /** They were on this task, not assigned to it. Absent on an old scrum. */
  addedTo?: boolean;
  /** Its column in the person's row, from 0. Null for done work and older scrums. */
  position?: number | null;
  /** What it was blocked on then. Null when it was not blocked, and on older scrums. */
  blockedOn?: { note: string; userName: string | null; taskTitle: string | null } | null;
}

/** One viewer's own arrangement of the scrum table: people top to bottom, tasks left to right. */
export interface ScrumOrder {
  people: string[];
  /** By person id. */
  tasks: Record<string, string[]>;
}

export interface Scrum extends ScrumSummary {
  participants: ScrumParticipant[];
  entries: ScrumEntry[];
}

export interface ContributionPerson {
  userId: string;
  name: string;
  teams: TeamRef[];
  completed: number;
  involved: number;
  open: number;
  addedTo: number;
}

export interface Contributions {
  scope: DashboardScope;
  teams: TeamRef[];
  from: string;
  to: string;
  people: ContributionPerson[];
}

export interface ContributionDetail {
  userId: string;
  name: string;
  from: string;
  to: string;
  completed: TaskSummary[];
  involved: TaskSummary[];
  open: TaskSummary[];
  addedTo: TaskSummary[];
}
