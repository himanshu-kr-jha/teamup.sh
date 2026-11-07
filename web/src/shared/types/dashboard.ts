/**
 * The task dashboard, scrum and contribution API, as the server answers it.
 * Ids and times are strings on the wire.
 */
export type DashboardScope = 'self' | 'team' | 'org';

export interface TeamRef {
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
