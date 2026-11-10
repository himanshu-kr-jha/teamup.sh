import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { Github, Loader2, Unlink } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { integrationsApi } from '@/shared/api/client';
import { BitbucketMark } from '@/features/admin/IntegrationsTab';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

type Provider = 'github' | 'bitbucket';

const NAMES: Record<Provider, string> = { github: 'GitHub', bitbucket: 'Bitbucket' };

const outcomeText = (provider: Provider, outcome: string): { ok: boolean; text: string } | undefined => {
  const name = NAMES[provider];
  return {
    linked: { ok: true, text: `Your ${name} account is linked. Your pull requests, reviews and commits are now known as yours.` },
    declined: { ok: false, text: `${name} was not linked: access was not allowed on ${name}’s page.` },
    taken: { ok: false, text: `That ${name} account is linked to somebody else here.` },
    failed: { ok: false, text: `${name} could not be linked. Please try again.` },
  }[outcome];
};

/**
 * The accounts you work with on GitHub and Bitbucket, which are rarely under
 * your work email: linking them is how your pull requests, reviews and commits
 * are known to be yours, and how a review asked of you there reaches you here.
 */
export function ConnectedAccounts() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [busy, setBusy] = useState<Provider | null>(null);
  const { data } = useQuery({ queryKey: ['linked-accounts'], queryFn: () => integrationsApi.myAccounts() });
  const accounts = data?.data ?? [];

  useEffect(() => {
    for (const provider of ['github', 'bitbucket'] as const) {
      const outcome = params.get(provider);
      if (!outcome) continue;
      const said = outcomeText(provider, outcome);
      if (said) (said.ok ? toast.success : toast.error)(said.text);
      void queryClient.invalidateQueries({ queryKey: ['linked-accounts'] });
      setParams(
        (current) => {
          const next = new URLSearchParams(current);
          next.delete(provider);
          return next;
        },
        { replace: true }
      );
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  // Nothing to link until the organization has connected GitHub or Bitbucket.
  if (accounts.length === 0) return null;

  const link = async (provider: Provider) => {
    setBusy(provider);
    try {
      const { data: res } = provider === 'github' ? await integrationsApi.linkGitHub() : await integrationsApi.linkBitbucket();
      window.location.assign(res.url);
    } catch (err) {
      toast.error(apiErrorMessage(err, `Could not reach ${NAMES[provider]}.`));
      setBusy(null);
    }
  };

  const unlink = async (provider: Provider) => {
    setBusy(provider);
    try {
      await integrationsApi.unlink(provider);
      await queryClient.invalidateQueries({ queryKey: ['linked-accounts'] });
      toast.success(`${NAMES[provider]} unlinked.`);
    } catch (err) {
      toast.error(apiErrorMessage(err, `Could not unlink ${NAMES[provider]}.`));
    } finally {
      setBusy(null);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Connected accounts</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <p className="text-muted-foreground">
          Link the accounts you work with, even if they are not under your work email, so your pull requests, reviews and
          commits show as yours and review requests reach you here.
        </p>
        {accounts.map((account) => (
          <div key={account.provider} className="flex flex-wrap items-center gap-3 rounded-md border border-border p-3">
            {account.provider === 'github' ? <Github className="h-5 w-5 shrink-0" aria-hidden="true" /> : <BitbucketMark />}
            <div className="min-w-0 flex-1">
              <p className="font-medium">{NAMES[account.provider]}</p>
              <p className="truncate text-muted-foreground">{account.login ? `Linked as @${account.login}` : 'Not linked'}</p>
            </div>
            {account.login ? (
              <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => unlink(account.provider)} aria-label={`Unlink ${NAMES[account.provider]}`}>
                <Unlink className="mr-1 h-4 w-4" aria-hidden="true" />
                Unlink
              </Button>
            ) : (
              <Button size="sm" disabled={busy !== null} onClick={() => link(account.provider)}>
                {busy === account.provider && <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" />}
                Link {NAMES[account.provider]}
              </Button>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
