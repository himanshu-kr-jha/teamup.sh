import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Copy, ExternalLink, GitBranch, KeyRound, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { accessApi, integrationsApi, type ProjectAccess, type ProjectRepo, type TaskBranch } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';
import { formatRelativeTime } from '@/shared/utils/cn';

const PROVIDER = { github: 'GitHub', bitbucket: 'Bitbucket' } as const;

/** What stands between somebody and starting a branch on a repository, if anything. */
export type BranchStanding = 'ok' | 'unlinked' | 'invited' | 'requested' | 'none';

/**
 * Whether somebody may start a branch on a repository: write access to the
 * project that reached that repository. The server decides again; this only
 * keeps a button from promising what it will refuse.
 */
export function branchStanding(access: ProjectAccess | undefined, userId: string | undefined, repo: ProjectRepo): BranchStanding {
  if (!access) return 'none';
  if (access.unlinked.includes(repo.provider)) return 'unlinked';
  const held = access.grants.find((g) => g.userId === userId);
  const there = held?.repositories.find((r) => r.repository === repo.fullName);
  if (held?.level === 'write' && there && (there.status === 'granted' || there.status === 'existing')) return 'ok';
  if (held?.level === 'write' && there?.status === 'invited') return 'invited';
  if (access.requests.some((r) => r.userId === userId && r.status === 'pending')) return 'requested';
  return 'none';
}

/** Copies a line and says so for a moment. */
function CopyButton({ text, label }: { text: string; label: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          toast.error('Could not copy. Select it and copy it instead.');
        }
      }}
      className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
    >
      {copied ? <Check className="h-3.5 w-3.5 text-emerald-600" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
    </button>
  );
}

/** The branches started for a task, with the line that gets one onto a laptop. */
export function TaskBranches({ branches }: { branches: TaskBranch[] }) {
  if (branches.length === 0) return null;
  return (
    <ul className="space-y-2" aria-label="Branches">
      {branches.map((b) => {
        const checkout = `git fetch origin && git switch ${b.name}`;
        return (
          <li key={b.id} className="space-y-1 rounded-md border border-border p-2" data-testid="task-branch">
            <div className="flex items-center gap-1.5">
              <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <a href={b.url} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 truncate font-mono text-xs font-medium hover:underline" title={b.name}>
                {b.name}
              </a>
              <ExternalLink className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />
            </div>
            <p className="text-xs text-muted-foreground">
              {b.repository}, from {b.base}
              {b.by.name ? `, by ${b.by.name}` : ''} {formatRelativeTime(b.createdAt)}
            </p>
            <div className="flex items-center gap-1 rounded bg-muted/50 py-0.5 pl-2 pr-0.5">
              <code className="min-w-0 flex-1 truncate text-[11px]" title={checkout}>
                {checkout}
              </code>
              <CopyButton text={checkout} label="Copy the command to switch to this branch" />
            </div>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * Starting development on a task: TeamUp makes its branch on one of its
 * project's repositories. Only somebody with write access to that repository may;
 * anybody else is offered to ask for it, which an administrator decides.
 */
export function StartDevelopment({
  taskId,
  taskKey,
  branch,
  projectId,
  projectName,
  repos,
  again = false,
}: {
  /** It has a branch on another of the project's repositories already. */
  again?: boolean;
  taskId: string;
  taskKey: string;
  branch: string;
  projectId: string;
  projectName: string;
  repos: ProjectRepo[];
}) {
  const { user } = useAuth();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [repoId, setRepoId] = useState(repos[0]?.id ?? '');
  const [base, setBase] = useState('');
  const [busy, setBusy] = useState(false);
  const { data: access } = useQuery({ queryKey: ['project-access', projectId], queryFn: () => accessApi.project(projectId) });
  const repo = repos.find((r) => r.id === repoId) ?? repos[0];
  if (!repo) return null;
  const standing = branchStanding(access, user?.id, repo);
  const provider = PROVIDER[repo.provider];

  const start = async () => {
    setBusy(true);
    try {
      const made = (await integrationsApi.startBranch(taskId, { repositoryId: repo.id, base: base.trim() || undefined })).data;
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['task-code', taskId] }),
        queryClient.invalidateQueries({ queryKey: ['task', taskId] }),
        queryClient.invalidateQueries({ queryKey: ['task-activities', taskId] }),
      ]);
      toast.success(`Branch ${made.name} is ready on ${made.repository}.`);
      setBase('');
    } catch (err) {
      const code = (err as { response?: { data?: { code?: string } } } | null)?.response?.data?.code;
      if (code === 'access_required') {
        await queryClient.invalidateQueries({ queryKey: ['project-access', projectId] });
        toast.error(`You need write access to ${projectName} to start a branch.`);
      } else if (code === 'provider_permission') {
        toast.error(
          repo.provider === 'github'
            ? 'TeamUp’s GitHub app may not create branches yet. An owner of the GitHub organization needs to allow it Contents: Read and write.'
            : 'TeamUp may not create branches on Bitbucket yet. A workspace owner needs to tick Repositories: Write on its consumer and sign in again.'
        );
      } else {
        toast.error(apiErrorMessage(err, 'Could not start the branch.'));
      }
    } finally {
      setBusy(false);
    }
  };

  const ask = async () => {
    setBusy(true);
    try {
      await accessApi.request(projectId, { level: 'write', reason: `To start the branch for ${taskKey}` });
      await queryClient.invalidateQueries({ queryKey: ['project-access'] });
      await queryClient.invalidateQueries({ queryKey: ['access-requests'] });
      toast.success('Asked for write access. An administrator will decide, and you will be told.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not ask for access.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-2" data-testid="start-development">
      <p className="text-xs font-medium text-muted-foreground">{again ? 'Start it on another repository too' : 'Start development'}</p>
      <div className="grid gap-2 sm:grid-cols-2">
        {repos.length > 1 ? (
          <select
            aria-label="Repository"
            value={repo.id}
            onChange={(e) => setRepoId(e.target.value)}
            className="min-w-0 rounded-md border border-input bg-background px-2 py-1.5 text-sm"
          >
            {repos.map((r) => (
              <option key={r.id} value={r.id}>
                {r.fullName}
              </option>
            ))}
          </select>
        ) : (
          <p className="min-w-0 truncate self-center text-sm" title={repo.fullName}>
            {repo.fullName}
          </p>
        )}
        <Input
          aria-label="From branch"
          placeholder="From the default branch"
          value={base}
          onChange={(e) => setBase(e.target.value)}
          className="h-8 text-sm"
          disabled={standing !== 'ok'}
        />
      </div>

      {standing === 'ok' ? (
        <Button size="sm" onClick={start} disabled={busy} className="w-full sm:w-auto">
          {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <GitBranch className="mr-2 h-4 w-4" aria-hidden="true" />}
          Create branch {branch}
        </Button>
      ) : (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-dashed border-border bg-muted/40 p-2 text-xs text-muted-foreground" data-testid="branch-standing">
          <KeyRound className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          <span className="min-w-0 flex-1">
            {standing === 'unlinked' ? (
              <>
                Link your {provider} account under <Link to="/settings" className="font-medium text-primary hover:underline">Settings</Link> first; access is given to it.
              </>
            ) : standing === 'invited' ? (
              <>Accept the invitation {provider} sent you to {repo.fullName}, then start the branch here.</>
            ) : standing === 'requested' ? (
              <>You asked for access to {projectName}. An administrator will decide.</>
            ) : (
              <>You need write access to {projectName} to start a branch.</>
            )}
          </span>
          {standing === 'none' && (
            <Button size="sm" variant="outline" onClick={ask} disabled={busy} className="h-7">
              Ask for write access
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
