import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, Check, FolderGit2, Github, Pencil, Plus } from 'lucide-react';
import { BitbucketMark } from './IntegrationsTab';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { Card, CardContent } from '@/shared/components/ui/card';
import { FormDialog } from '@/shared/components/FormDialog';
import { projectsApi, integrationsApi, type Project } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';

/** A key suggested from a name: Photon is PHO, Beta backend is BB. */
export function suggestKey(name: string): string {
  const words = name.toUpperCase().replace(/[^A-Z0-9 ]/g, ' ').split(/\s+/).filter(Boolean);
  if (words.length === 0) return '';
  const key = words.length === 1 ? words[0].slice(0, 3) : words.map((w) => w[0]).join('').slice(0, 5);
  return /^[A-Z]/.test(key) ? key : '';
}

const KEY = /^[A-Z][A-Z0-9]{1,9}$/;

/** The organization's projects, the repositories each is made of, and their task keys. */
export function ProjectsTab({ canManage }: { canManage: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.list() });
  const projects = data?.data ?? [];
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Project | null>(null);
  const [choosing, setChoosing] = useState<Project | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['projects'] });

  const archive = async (p: Project) => {
    try {
      await projectsApi.archive(p.id);
      await refresh();
      toast.success(`${p.name} archived. Its tasks keep their keys.`);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not archive that project.'));
    }
  };

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading projects…</p>;

  return (
    <div className="max-w-3xl space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <p className="mr-auto text-sm text-muted-foreground">
          A project&apos;s tasks are keyed with its key, such as <code className="rounded bg-muted px-1">PHO-12</code>. Name the
          key in a branch, commit or pull request and it shows on the task.
        </p>
        {canManage && (
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="mr-1 h-4 w-4" aria-hidden="true" />
            New project
          </Button>
        )}
      </div>

      {projects.length === 0 && (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            No projects yet.{canManage ? ' Make one for each product, such as Photon or Cortex.' : ''}
          </CardContent>
        </Card>
      )}

      <ul className="space-y-3" aria-label="Projects">
        {projects.map((p) => (
          <li key={p.id}>
            <Card>
              <CardContent className="space-y-3 p-4">
                <div className="flex flex-wrap items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <h3 className="flex flex-wrap items-center gap-2 font-semibold">
                      <span className="break-anywhere">{p.name}</span>
                      <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs font-medium text-muted-foreground">{p.key}</span>
                    </h3>
                    {p.description && <p className="break-anywhere text-sm text-muted-foreground">{p.description}</p>}
                    <p className="text-xs text-muted-foreground">
                      {p.openTasks === 1 ? '1 open task' : `${p.openTasks} open tasks`}
                    </p>
                  </div>
                  {canManage && (
                    <div className="flex flex-wrap gap-1">
                      <Button size="sm" variant="outline" onClick={() => setChoosing(p)}>
                        <FolderGit2 className="mr-1 h-4 w-4" aria-hidden="true" />
                        Repositories
                      </Button>
                      <Button size="sm" variant="ghost" aria-label={`Rename ${p.name}`} onClick={() => setEditing(p)}>
                        <Pencil className="h-4 w-4" aria-hidden="true" />
                      </Button>
                      <Button size="sm" variant="ghost" aria-label={`Archive ${p.name}`} onClick={() => archive(p)}>
                        <Archive className="h-4 w-4" aria-hidden="true" />
                      </Button>
                    </div>
                  )}
                </div>
                {p.repositories.length > 0 ? (
                  <ul className="flex flex-wrap gap-1.5" aria-label={`${p.name} repositories`}>
                    {p.repositories.map((r) => (
                      <li key={r.id}>
                        <a
                          href={r.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-xs hover:bg-accent"
                        >
                          {r.provider === 'bitbucket' ? <BitbucketMark small /> : <Github className="h-3 w-3 text-muted-foreground" aria-hidden="true" />}
                          {r.fullName}
                        </a>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="text-xs text-muted-foreground">No repositories yet.</p>
                )}
              </CardContent>
            </Card>
          </li>
        ))}
      </ul>

      <ProjectDialog open={creating} onOpenChange={setCreating} onDone={refresh} />
      <ProjectDialog open={!!editing} project={editing} onOpenChange={(o) => !o && setEditing(null)} onDone={refresh} />
      <RepositoriesDialog project={choosing} onClose={() => setChoosing(null)} onDone={refresh} />
    </div>
  );
}

function ProjectDialog({
  open,
  project,
  onOpenChange,
  onDone,
}: {
  open: boolean;
  project?: Project | null;
  onOpenChange: (open: boolean) => void;
  onDone: () => void;
}) {
  const [name, setName] = useState('');
  const [key, setKey] = useState('');
  const [keyTouched, setKeyTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [seeded, setSeeded] = useState<string | null>(null);
  const id = project?.id ?? 'new';
  if (open && seeded !== id) {
    setSeeded(id);
    setName(project?.name ?? '');
    setKey(project?.key ?? '');
    setKeyTouched(!!project);
    setDescription(project?.description ?? '');
  }
  if (!open && seeded !== null) setSeeded(null);
  const shownKey = keyTouched ? key : suggestKey(name);

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={project ? `Rename ${project.name}` : 'New project'}
      description={project ? 'The key stays the same, so the keys of its tasks and the branches named after them keep working.' : 'Its key starts the key of every task in it, and cannot be changed later.'}
      submitLabel={project ? 'Save' : 'Create project'}
      disabledReason={!name.trim() ? 'Give it a name' : !project && !KEY.test(shownKey) ? 'A key is 2 to 10 capitals or digits, starting with a letter' : undefined}
      onSubmit={async () => {
        if (project) await projectsApi.update(project.id, { name: name.trim(), description: description.trim() });
        else await projectsApi.create({ name: name.trim(), key: shownKey, description: description.trim() });
        onDone();
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="projectName">Name</Label>
        <Input id="projectName" value={name} onChange={(e) => setName(e.target.value)} placeholder="Photon" autoFocus />
      </div>
      {!project && (
        <div className="space-y-2">
          <Label htmlFor="projectKey">Key</Label>
          <Input
            id="projectKey"
            value={shownKey}
            onChange={(e) => {
              setKeyTouched(true);
              setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 10));
            }}
            placeholder="PHO"
            className="font-mono uppercase"
          />
          <p className="text-xs text-muted-foreground">
            Tasks will be {shownKey || 'KEY'}-1, {shownKey || 'KEY'}-2… and a branch named {(shownKey || 'key').toLowerCase()}-1-fix-login is linked to {shownKey || 'KEY'}-1.
          </p>
        </div>
      )}
      <div className="space-y-2">
        <Label htmlFor="projectDescription">Description (optional)</Label>
        <Textarea id="projectDescription" rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />
      </div>
    </FormDialog>
  );
}

function RepositoriesDialog({ project, onClose, onDone }: { project: Project | null; onClose: () => void; onDone: () => void }) {
  const { data, isLoading } = useQuery({ queryKey: ['repositories'], queryFn: () => integrationsApi.repositories(), enabled: !!project });
  const repos = useMemo(() => data?.data ?? [], [data]);
  const { data: projectsData } = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.list(), enabled: !!project });
  const projectName = (id?: string) => projectsData?.data.find((p) => p.id === id)?.name;
  const [picked, setPicked] = useState<string[] | null>(null);
  const chosen = picked ?? (project ? repos.filter((r) => r.projectId === project.id).map((r) => r.id) : []);
  const [query, setQuery] = useState('');
  const shown = repos.filter((r) => r.fullName.toLowerCase().includes(query.trim().toLowerCase()));

  return (
    <FormDialog
      open={!!project}
      onOpenChange={(o) => {
        if (!o) {
          setPicked(null);
          setQuery('');
          onClose();
        }
      }}
      title={project ? `${project.name} repositories` : 'Repositories'}
      description="Tick the repositories this project is made of. A repository is in one project at a time."
      submitLabel="Save"
      onSubmit={async () => {
        await projectsApi.setRepositories(project!.id, chosen);
        setPicked(null);
        onDone();
      }}
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading repositories…</p>
      ) : repos.length === 0 ? (
        <p className="text-sm text-muted-foreground">No repositories yet. Connect GitHub or Bitbucket under Integrations first.</p>
      ) : (
        <div className="space-y-2">
          <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Find a repository" aria-label="Find a repository"
            onKeyDown={(e) => e.key === 'Enter' && e.preventDefault()} />
          <ul className="max-h-72 overflow-y-auto rounded-md border border-border p-1" aria-label="Repositories">
            {shown.map((r) => {
              const on = chosen.includes(r.id);
              const elsewhere = r.projectId && r.projectId !== project?.id ? projectName(r.projectId) : undefined;
              return (
                <li key={r.id}>
                  <label className={cn('flex cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 hover:bg-accent', on && 'bg-primary/5')}>
                    <span className="relative flex h-4 w-4 shrink-0">
                      <input
                        type="checkbox"
                        checked={on}
                        onChange={() => setPicked(on ? chosen.filter((x) => x !== r.id) : [...chosen, r.id])}
                        className="peer h-4 w-4 cursor-pointer appearance-none rounded border border-input bg-background checked:border-primary checked:bg-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      />
                      <Check className="pointer-events-none absolute inset-0 m-auto h-3 w-3 text-primary-foreground opacity-0 peer-checked:opacity-100" strokeWidth={3} aria-hidden="true" />
                    </span>
                    {r.provider === 'bitbucket' ? <BitbucketMark small /> : <Github className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm">{r.fullName}</span>
                      {elsewhere && !on && <span className="block text-xs text-muted-foreground">In {elsewhere} now</span>}
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
          <p className="text-xs text-muted-foreground">{chosen.length === 1 ? '1 chosen.' : `${chosen.length} chosen.`}</p>
        </div>
      )}
    </FormDialog>
  );
}
