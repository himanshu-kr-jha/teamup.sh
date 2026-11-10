import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { Github, Loader2, Unlink } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { integrationsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

const OUTCOMES: Record<string, { ok: boolean; text: string }> = {
  linked: { ok: true, text: 'Your GitHub account is linked. Your pull requests, reviews and commits are now known as yours.' },
  declined: { ok: false, text: 'GitHub was not linked: access was not allowed on GitHub’s page.' },
  taken: { ok: false, text: 'That GitHub account is linked to somebody else here.' },
  failed: { ok: false, text: 'GitHub could not be linked. Please try again.' },
};

/**
 * Your own GitHub account, which is rarely under your work email: linking it
 * is how your pull requests, reviews and commits are known to be yours, and
 * how a review asked of you on GitHub reaches you here.
 */
export function ConnectedAccounts() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [busy, setBusy] = useState(false);
  const { data } = useQuery({ queryKey: ['linked-accounts'], queryFn: () => integrationsApi.myAccounts() });
  const github = data?.data.find((a) => a.provider === 'github');
  const outcome = params.get('github');

  useEffect(() => {
    if (!outcome) return;
    const said = OUTCOMES[outcome];
    if (said) (said.ok ? toast.success : toast.error)(said.text);
    void queryClient.invalidateQueries({ queryKey: ['linked-accounts'] });
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.delete('github');
        return next;
      },
      { replace: true }
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outcome]);

  // Nothing to link until the organization has connected GitHub.
  if (!github) return null;

  const link = async () => {
    setBusy(true);
    try {
      const { data: res } = await integrationsApi.linkGitHub();
      window.location.assign(res.url);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not reach GitHub.'));
      setBusy(false);
    }
  };

  const unlink = async () => {
    setBusy(true);
    try {
      await integrationsApi.unlink('github');
      await queryClient.invalidateQueries({ queryKey: ['linked-accounts'] });
      toast.success('GitHub unlinked.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not unlink GitHub.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Connected accounts</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <p className="text-muted-foreground">
          Link the GitHub account you work with, even if it is not under your work email, so your pull requests, reviews
          and commits show as yours and review requests reach you here.
        </p>
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-border p-3">
          <Github className="h-5 w-5 shrink-0" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="font-medium">GitHub</p>
            <p className="truncate text-muted-foreground">{github.login ? `Linked as @${github.login}` : 'Not linked'}</p>
          </div>
          {github.login ? (
            <Button size="sm" variant="outline" disabled={busy} onClick={unlink}>
              <Unlink className="mr-1 h-4 w-4" aria-hidden="true" />
              Unlink
            </Button>
          ) : (
            <Button size="sm" disabled={busy} onClick={link}>
              {busy ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" /> : <Github className="mr-1 h-4 w-4" aria-hidden="true" />}
              Link GitHub
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
