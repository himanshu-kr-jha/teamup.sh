import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Check, CircleDot, Copy, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, X } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { integrationsApi, type CodePerson, type CodePullRequest } from '@/shared/api/client';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { useToast } from '@/shared/hooks/useToast';
import { cn, formatRelativeTime } from '@/shared/utils/cn';
import { liveTaskQuery } from './liveQuery';

/** The branch name that links work to this task: pho-12-fix-sign-in. */
export function branchFor(key: string, title: string): string {
  const words = title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .split('-')
    .slice(0, 6)
    .join('-');
  return [key.toLowerCase(), words].filter(Boolean).join('-');
}

const PR_STATE: Record<CodePullRequest['state'], { label: string; icon: typeof GitPullRequest; tone: string }> = {
  open: { label: 'Open', icon: GitPullRequest, tone: 'text-emerald-600 bg-emerald-50 dark:bg-emerald-950/50' },
  draft: { label: 'Draft', icon: GitPullRequestDraft, tone: 'text-muted-foreground bg-muted' },
  merged: { label: 'Merged', icon: GitMerge, tone: 'text-violet-600 bg-violet-50 dark:bg-violet-950/50' },
  closed: { label: 'Closed', icon: GitPullRequestClosed, tone: 'text-red-600 bg-red-50 dark:bg-red-950/50' },
};

const REVIEW: Record<string, string> = {
  requested: 'review requested',
  approved: 'approved',
  changes_requested: 'asked for changes',
  commented: 'commented',
};

function Who({ person }: { person: CodePerson }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      <PersonAvatar userId={person.userId} name={person.name || person.login} className="h-4 w-4" fallbackClassName="text-[8px]" decorative />
      <span className="truncate">{person.name || `@${person.login}`}</span>
    </span>
  );
}

/**
 * The work done for a task in GitHub: its pull requests, who made them and
 * who is reviewing, and its commits. Anything that names the task's key is
 * here; the branch to use is offered to copy.
 */
export function DevelopmentCard({ taskId, taskKey, title }: { taskId: string; taskKey: string; title: string }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const { data } = useQuery({
    queryKey: ['task-code', taskId],
    queryFn: () => integrationsApi.taskCode(taskId).then((r) => r.data),
    ...liveTaskQuery,
  });
  const branch = branchFor(taskKey, title);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(branch);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error('Could not copy. Select the name and copy it instead.');
    }
  };

  const prs = data?.pullRequests ?? [];
  const commits = data?.commits ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Development</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground">Branch for this task</p>
          <div className="flex items-center gap-1 rounded-md border border-border bg-muted/40 py-1 pl-2 pr-1">
            <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            <code className="min-w-0 flex-1 truncate text-xs" title={branch}>{branch}</code>
            <button
              type="button"
              onClick={copy}
              aria-label="Copy branch name"
              className="flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              {copied ? <Check className="h-3.5 w-3.5 text-emerald-600" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
            </button>
          </div>
          <p className="text-xs text-muted-foreground">
            Or put <span className="font-mono">{taskKey}</span> in a pull request&apos;s title or a commit message.
          </p>
        </div>

        {data && !data.connected && prs.length === 0 && (
          <p className="text-xs text-muted-foreground">Once an administrator connects GitHub or Bitbucket, pull requests and commits show here.</p>
        )}

        {prs.length > 0 && (
          <ul className="space-y-3" aria-label="Pull requests">
            {prs.map((pr) => {
              const state = PR_STATE[pr.state];
              const Icon = state.icon;
              return (
                <li key={pr.id} className="space-y-1.5 rounded-md border border-border p-2">
                  <div className="flex items-start gap-2">
                    <span className={cn('mt-0.5 inline-flex shrink-0 items-center gap-1 rounded-full px-1.5 py-0.5 text-[11px] font-medium', state.tone)}>
                      <Icon className="h-3 w-3" aria-hidden="true" />
                      {state.label}
                    </span>
                    <a href={pr.url} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 font-medium hover:underline">
                      <span className="break-anywhere">{pr.title}</span>
                    </a>
                  </div>
                  <p className="flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
                    <span className="truncate">{pr.repository}#{pr.number}</span>
                    <span aria-hidden="true">·</span>
                    <Who person={pr.author} />
                    <span aria-hidden="true">·</span>
                    <span>{formatRelativeTime(pr.mergedAt ?? pr.openedAt)}</span>
                    {pr.checks === 'failing' && (
                      <span className="inline-flex items-center gap-0.5 text-red-600">
                        <X className="h-3 w-3" aria-hidden="true" />
                        checks failing
                      </span>
                    )}
                    {pr.checks === 'passing' && (
                      <span className="inline-flex items-center gap-0.5 text-emerald-600">
                        <Check className="h-3 w-3" aria-hidden="true" />
                        checks passing
                      </span>
                    )}
                  </p>
                  {pr.reviewers.length > 0 && (
                    <ul className="space-y-0.5 text-xs" aria-label="Reviewers">
                      {pr.reviewers.map((r) => (
                        <li key={r.login} className="flex items-center gap-1.5">
                          {r.state === 'approved' ? (
                            <Check className="h-3 w-3 text-emerald-600" aria-hidden="true" />
                          ) : r.state === 'changes_requested' ? (
                            <X className="h-3 w-3 text-red-600" aria-hidden="true" />
                          ) : (
                            <CircleDot className="h-3 w-3 text-amber-500" aria-hidden="true" />
                          )}
                          <Who person={r} />
                          <span className="text-muted-foreground">{REVIEW[r.state] ?? r.state}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
        )}

        {commits.length > 0 && (
          <div className="space-y-1">
            <p className="text-xs font-medium text-muted-foreground">Commits</p>
            <ul className="space-y-1.5" aria-label="Commits">
              {commits.slice(0, 10).map((c) => (
                <li key={c.sha} className="flex items-start gap-2 text-xs">
                  <GitCommitHorizontal className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <div className="min-w-0 flex-1">
                    <a href={c.url} target="_blank" rel="noopener noreferrer" className="block truncate hover:underline" title={c.message}>
                      {c.message.split('\n')[0]}
                    </a>
                    <p className="flex flex-wrap items-center gap-x-1.5 text-muted-foreground">
                      <code>{c.sha.slice(0, 7)}</code>
                      <span aria-hidden="true">·</span>
                      <Who person={c.author} />
                      <span aria-hidden="true">·</span>
                      <span>{formatRelativeTime(c.committedAt)}</span>
                    </p>
                  </div>
                </li>
              ))}
            </ul>
            {commits.length > 10 && <p className="text-xs text-muted-foreground">and {commits.length - 10} more.</p>}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
