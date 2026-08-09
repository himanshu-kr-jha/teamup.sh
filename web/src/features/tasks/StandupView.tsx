import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { AlertTriangle, CheckCircle2, ChevronDown, ChevronRight, Circle, Clock } from 'lucide-react';
import { Badge } from '@/shared/components/ui/badge';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { tasksApi } from '@/shared/api/client';
import { cn } from '@/shared/utils/cn';

interface Task {
  id: string;
  title: string;
  status: string;
  priority: string;
  assigneeId?: string;
  assigneeName?: string;
  dueDate?: string;
  updatedAt: string;
  completedAt?: string;
}

/** How far back counts as "since we last spoke". */
const RECENT_DAYS = 1;

/**
 * The tasks tab arranged for a stand-up: one block per person, in the order you
 * would go round the room, each showing what they are on, what is stuck, and
 * what closed since yesterday. The board is the wrong shape for this — it is
 * organised by column, so working out what one person is doing means reading
 * every column.
 */
export function StandupView({ teamId, assigneeId }: { teamId: string; assigneeId: string }) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  const { data: teamsData } = useQuery({
    queryKey: ['managed-teams'],
    queryFn: () => tasksApi.managedTeams(),
  });
  const { data: tasksData, isLoading } = useQuery({
    queryKey: ['tasks', { scope: 'team', teamId, assigneeId, standup: true }],
    queryFn: () =>
      tasksApi.list({
        scope: 'team',
        teamId: teamId !== 'all' ? teamId : undefined,
        assigneeId: assigneeId !== 'all' ? assigneeId : undefined,
      }),
  });

  const tasks = (tasksData?.data ?? []) as Task[];

  // Everybody on the teams in view, so somebody with nothing on still comes up
  // in the round — an empty block is itself worth seeing at a stand-up.
  const people = useMemo(() => {
    const teams = teamsData?.data ?? [];
    const inScope = teamId === 'all' ? teams : teams.filter((t: any) => t.id === teamId);
    const byId = new Map<string, { userId: string; name: string }>();
    inScope.forEach((t: any) =>
      t.members.forEach((m: any) => {
        if (assigneeId === 'all' || m.userId === assigneeId) byId.set(m.userId, m);
      })
    );
    return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name));
  }, [teamsData, teamId, assigneeId]);

  const since = useMemo(() => {
    const d = new Date();
    d.setDate(d.getDate() - RECENT_DAYS);
    return d;
  }, []);

  const forPerson = (userId: string) => {
    const theirs = tasks.filter((t) => t.assigneeId === userId);
    return {
      inProgress: theirs.filter((t) => t.status === 'in_progress'),
      inReview: theirs.filter((t) => t.status === 'in_review'),
      blocked: theirs.filter((t) => t.status === 'blocked'),
      todo: theirs.filter((t) => t.status === 'todo'),
      doneRecently: theirs.filter(
        (t) => t.status === 'done' && new Date(t.completedAt ?? t.updatedAt) >= since
      ),
    };
  };

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading the team's work…</p>;
  }

  if (people.length === 0) {
    return (
      <Card>
        <CardContent className="py-10 text-center text-muted-foreground">
          <p>Nobody to go round yet.</p>
          <p className="mt-1 text-sm">
            Add people to a team you manage, in Organization → Teams.
          </p>
        </CardContent>
      </Card>
    );
  }

  return (
    <div className="space-y-3">
      {people.map((person) => {
        const work = forPerson(person.userId);
        const isCollapsed = collapsed[person.userId];
        const active = work.inProgress.length + work.inReview.length;

        return (
          <Card key={person.userId}>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 py-3">
              <button
                className="flex min-w-0 flex-1 items-center gap-3 text-left"
                onClick={() =>
                  setCollapsed((all) => ({ ...all, [person.userId]: !all[person.userId] }))
                }
                aria-expanded={!isCollapsed}
              >
                {isCollapsed ? (
                  <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                ) : (
                  <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                )}
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10">
                  <span className="text-sm font-medium text-primary">
                    {person.name.charAt(0).toUpperCase()}
                  </span>
                </div>
                <CardTitle className="truncate text-base">{person.name}</CardTitle>
              </button>

              {/* The shape of their week at a glance, without expanding. */}
              <div className="flex shrink-0 items-center gap-2">
                {work.blocked.length > 0 && (
                  <Badge variant="destructive" className="gap-1">
                    <AlertTriangle className="h-3 w-3" aria-hidden="true" />
                    {work.blocked.length} blocked
                  </Badge>
                )}
                <Badge variant="secondary">{active} active</Badge>
                {work.doneRecently.length > 0 && (
                  <Badge variant="outline" className="gap-1 text-green-600">
                    <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
                    {work.doneRecently.length}
                  </Badge>
                )}
              </div>
            </CardHeader>

            {!isCollapsed && (
              <CardContent className="space-y-3 pt-0">
                {active === 0 && work.blocked.length === 0 && work.doneRecently.length === 0 && (
                  <p className="text-sm text-muted-foreground">
                    Nothing assigned{work.todo.length > 0 ? ' and started' : ''}.
                    {work.todo.length > 0 && ` ${work.todo.length} waiting to be picked up.`}
                  </p>
                )}

                <Group title="Blocked" tone="blocked" tasks={work.blocked} />
                <Group title="In progress" tone="active" tasks={work.inProgress} />
                <Group title="In review" tone="active" tasks={work.inReview} />
                <Group title="Closed since yesterday" tone="done" tasks={work.doneRecently} />
                <Group title="Not started" tone="idle" tasks={work.todo} />
              </CardContent>
            )}
          </Card>
        );
      })}
    </div>
  );
}

function Group({
  title,
  tasks,
  tone,
}: {
  title: string;
  tasks: Task[];
  tone: 'blocked' | 'active' | 'done' | 'idle';
}) {
  if (tasks.length === 0) return null;

  const Icon =
    tone === 'blocked' ? AlertTriangle : tone === 'done' ? CheckCircle2 : tone === 'active' ? Clock : Circle;

  return (
    <div>
      <p className="mb-1 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        <Icon
          className={cn(
            'h-3.5 w-3.5',
            tone === 'blocked' && 'text-destructive',
            tone === 'done' && 'text-green-600'
          )}
          aria-hidden="true"
        />
        {title}
        <span className="font-normal normal-case">({tasks.length})</span>
      </p>
      <ul className="space-y-1">
        {tasks.map((task) => (
          <li key={task.id}>
            <Button
              asChild
              variant="ghost"
              className="h-auto w-full justify-start gap-2 px-2 py-1.5 text-left font-normal"
            >
              <Link to={`/tasks/${task.id}`}>
                <span className="min-w-0 flex-1 truncate text-sm">{task.title}</span>
                {task.priority === 'urgent' || task.priority === 'high' ? (
                  <Badge variant="outline" className="shrink-0 text-xs capitalize">
                    {task.priority}
                  </Badge>
                ) : null}
                {task.dueDate && (
                  <span
                    className={cn(
                      'shrink-0 text-xs',
                      new Date(task.dueDate) < new Date() && tone !== 'done'
                        ? 'font-medium text-destructive'
                        : 'text-muted-foreground'
                    )}
                  >
                    {new Date(task.dueDate).toLocaleDateString(undefined, {
                      day: 'numeric',
                      month: 'short',
                    })}
                  </span>
                )}
              </Link>
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}
