import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderInput } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { FormDialog } from '@/shared/components/FormDialog';
import { projectsApi, tasksApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';

/**
 * The project a task belongs to, and moving it into one. Research or a bug being
 * looked into is often filed with no project; once it is a project's work it
 * moves there and takes the project's key, TASK-7 becoming PHO-31, keeping its
 * whole story. Branches and commits that named the old key still link to it.
 */
export function ProjectCard({
  taskId,
  taskKey,
  projectId,
  projectName,
}: {
  taskId: string;
  taskKey?: string;
  projectId?: string;
  projectName?: string;
}) {
  const [moving, setMoving] = useState(false);
  const [chosen, setChosen] = useState('');
  // Said as it was when the dialog opened: moved, the task is in a project, and the
  // title must not change under the dialog as it closes.
  const [wasIn, setWasIn] = useState(false);
  const queryClient = useQueryClient();
  const toast = useToast();
  const { data } = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.list() });
  const projects = data?.data ?? [];
  const elsewhere = projects.filter((p) => p.id !== projectId);
  const target = projects.find((p) => p.id === chosen);

  // Nowhere to move it: no projects, or only the one it is in.
  if (!projectId && elsewhere.length === 0) return null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Project</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {projectName ? (
          <p className="font-medium">{projectName}</p>
        ) : (
          <p className="text-sm text-muted-foreground">Not in a project yet. Once it is a project&apos;s work, move it there to start its branch.</p>
        )}
        {elsewhere.length > 0 && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setChosen(elsewhere[0].id);
              setWasIn(!!projectId);
              setMoving(true);
            }}
          >
            <FolderInput className="mr-2 h-4 w-4" aria-hidden="true" />
            {projectId ? 'Move to another project' : 'Move to a project'}
          </Button>
        )}
      </CardContent>
      <FormDialog
        open={moving}
        onOpenChange={setMoving}
        title={wasIn ? 'Move to another project' : 'Move to a project'}
        description="It keeps its comments, history, files and sub-tasks."
        submitLabel="Move"
        disabledReason={chosen ? undefined : 'Choose a project'}
        onSubmit={async () => {
          // A refusal is shown in the dialog, which stays open.
          const moved = (await tasksApi.moveToProject(taskId, chosen)).data as { key: string; projectName?: string };
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: ['task', taskId] }),
            queryClient.invalidateQueries({ queryKey: ['tasks'] }),
            queryClient.invalidateQueries({ queryKey: ['task-activities', taskId] }),
            queryClient.invalidateQueries({ queryKey: ['projects'] }),
          ]);
          toast.success(
            taskKey ? `Moved to ${moved.projectName ?? 'the project'} as ${moved.key}. ${taskKey} still links here.` : `Moved as ${moved.key}.`
          );
        }}
      >
        <div className="space-y-2">
          <label htmlFor="move-project" className="text-sm font-medium">
            Project
          </label>
          <select
            id="move-project"
            value={chosen}
            onChange={(e) => setChosen(e.target.value)}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          >
            {elsewhere.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.key})
              </option>
            ))}
          </select>
          {target && taskKey && (
            <p className="text-sm text-muted-foreground">
              It becomes <span className="font-mono">{target.key}-…</span>. Anything that names <span className="font-mono">{taskKey}</span>, a branch or a commit, still links here.
            </p>
          )}
        </div>
      </FormDialog>
    </Card>
  );
}
