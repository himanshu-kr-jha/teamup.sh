import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Check, CheckCircle2, Copy, ExternalLink, Github, Loader2, RefreshCw, Unplug } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { integrationsApi, type CodeConnection } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';

/**
 * Sends the browser to GitHub with the app's manifest. GitHub only takes it as
 * a form posted to its own page, where the admin confirms making the app.
 */
function postManifest(action: string, manifest: string) {
  const form = document.createElement('form');
  form.method = 'post';
  form.action = action;
  const field = document.createElement('input');
  field.type = 'hidden';
  field.name = 'manifest';
  field.value = manifest;
  form.appendChild(field);
  document.body.appendChild(form);
  form.submit();
}

const OUTCOMES: Record<string, { ok: boolean; text: string }> = {
  connected: { ok: true, text: 'GitHub is connected. Its repositories are listed under Projects.' },
  failed: { ok: false, text: 'GitHub could not be connected. Please try again.' },
};

const BITBUCKET_OUTCOMES: Record<string, { ok: boolean; text: string }> = {
  connected: { ok: true, text: 'Bitbucket is connected. Its repositories are listed under Projects.' },
  'not-owner': { ok: false, text: 'Only an owner of the Bitbucket workspace can connect it. Sign in to Bitbucket as one and try again.' },
  declined: { ok: false, text: 'Bitbucket was not connected: access was not allowed on Bitbucket’s page.' },
  failed: { ok: false, text: 'Bitbucket could not be connected. Check the key and secret, and the callback URL, then try again.' },
};

/** GitHub and Bitbucket, connected once for the whole organization. */
export function IntegrationsTab({
  outcome,
  bitbucketOutcome,
  onOutcomeSeen,
}: {
  outcome: string | null;
  bitbucketOutcome?: string | null;
  onOutcomeSeen: () => void;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['integrations'], queryFn: () => integrationsApi.connections() });
  const github = data?.data.find((c) => c.provider === 'github');
  const bitbucket = data?.data.find((c) => c.provider === 'bitbucket');

  useEffect(() => {
    if (!outcome && !bitbucketOutcome) return;
    const said = outcome ? OUTCOMES[outcome] : BITBUCKET_OUTCOMES[bitbucketOutcome!];
    if (said) (said.ok ? toast.success : toast.error)(said.text);
    void queryClient.invalidateQueries({ queryKey: ['integrations'] });
    onOutcomeSeen();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outcome, bitbucketOutcome]);

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading integrations…</p>;

  return (
    <div className="max-w-3xl space-y-6">
      <p className="text-sm text-muted-foreground">
        Connect your organization&apos;s code once. Pull requests and commits that name a task, such as{' '}
        <code className="rounded bg-muted px-1">PHO-12</code>, show on it and move it along, and reviewers are told here when
        their review is wanted. Each person then links their own account under Settings.
      </p>
      {github && <GitHubCard connection={github} />}
      {bitbucket && <BitbucketCard connection={bitbucket} />}
    </div>
  );
}

export function BitbucketMark({ small }: { small?: boolean } = {}) {
  return (
    <span
      className={cn(
        'flex shrink-0 items-center justify-center rounded bg-[#2684FF] font-bold text-white',
        small ? 'h-3.5 w-3.5 text-[8px]' : 'h-5 w-5 text-[11px]'
      )}
      aria-hidden="true"
    >
      B
    </span>
  );
}

function GitHubCard({ connection }: { connection: CodeConnection }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [organization, setOrganization] = useState('');
  const [busy, setBusy] = useState<'connect' | 'sync' | 'disconnect' | null>(null);
  const [confirming, setConfirming] = useState(false);

  const connect = async () => {
    setBusy('connect');
    try {
      const { data } = await integrationsApi.githubManifest(organization);
      postManifest(data.action, data.manifest);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not start connecting GitHub.'));
      setBusy(null);
    }
  };

  const sync = async () => {
    setBusy('sync');
    try {
      const { data } = await integrationsApi.sync('github');
      queryClient.setQueryData(['integrations'], (old: any) => (old ? { ...old, data: data.connections } : old));
      await queryClient.invalidateQueries({ queryKey: ['repositories'] });
      toast.success('Repositories brought up to date.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not read the repositories from GitHub.'));
    } finally {
      setBusy(null);
    }
  };

  const disconnect = async () => {
    setBusy('disconnect');
    try {
      await integrationsApi.disconnect('github');
      await queryClient.invalidateQueries({ queryKey: ['integrations'] });
      await queryClient.invalidateQueries({ queryKey: ['repositories'] });
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      toast.success('GitHub disconnected. Remove the app on GitHub too, from its settings page.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not disconnect GitHub.'));
    } finally {
      setBusy(null);
      setConfirming(false);
    }
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-3 space-y-0">
        <Github className="h-5 w-5" aria-hidden="true" />
        <CardTitle className="text-base">GitHub</CardTitle>
        {connection.status === 'active' && (
          <span className="ml-auto flex items-center gap-1 text-xs font-medium text-emerald-600">
            <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
            Connected
          </span>
        )}
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {!connection.available ? (
          <p role="alert" className="text-muted-foreground">
            The server has no TEAMUP_AUTH_SECRET, so GitHub&apos;s keys could not be kept safe. Set it, then come back.
          </p>
        ) : connection.status === 'none' ? (
          <>
            <ol className="list-decimal space-y-1 pl-5 text-muted-foreground">
              <li>Name your GitHub organization below and press Connect.</li>
              <li>GitHub shows the app TeamUp needs. Press Create, as an owner of the organization.</li>
              <li>Choose the repositories TeamUp may see, and Install.</li>
            </ol>
            <form
              className="flex flex-col gap-2 sm:flex-row sm:items-end"
              onSubmit={(e) => {
                e.preventDefault();
                void connect();
              }}
            >
              <div className="min-w-0 flex-1 space-y-2">
                <Label htmlFor="githubOrg">GitHub organization</Label>
                <Input
                  id="githubOrg"
                  value={organization}
                  onChange={(e) => setOrganization(e.target.value)}
                  placeholder="cognecto, as in github.com/cognecto"
                />
              </div>
              <Button type="submit" disabled={!organization.trim() || busy !== null}>
                {busy === 'connect' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <Github className="mr-2 h-4 w-4" aria-hidden="true" />}
                Connect GitHub
              </Button>
            </form>
          </>
        ) : (
          <>
            <p>
              {connection.status === 'pending' ? (
                <>The app is made on GitHub but not installed yet.</>
              ) : (
                <>
                  Connected to <span className="font-medium">{connection.account}</span>, seeing{' '}
                  {connection.repositories === 1 ? '1 repository' : `${connection.repositories} repositories`}.
                </>
              )}
            </p>
            {connection.lastError && (
              <p role="alert" className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-50 p-2 text-amber-950 dark:bg-amber-950/40 dark:text-amber-100">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                {connection.lastError}
              </p>
            )}
            {confirming ? (
              <div className="flex flex-wrap items-center gap-2">
                <span className="mr-auto">Disconnect GitHub? Links on tasks and linked accounts are removed.</span>
                <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => setConfirming(false)}>
                  Keep it
                </Button>
                <Button variant="destructive" size="sm" disabled={busy !== null} onClick={disconnect}>
                  {busy === 'disconnect' && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
                  Disconnect
                </Button>
              </div>
            ) : (
              <div className="flex flex-wrap gap-2">
                {connection.installUrl && (
                  <Button asChild size="sm" variant={connection.status === 'active' ? 'outline' : 'default'}>
                    <a href={connection.installUrl}>
                      <ExternalLink className="mr-2 h-4 w-4" aria-hidden="true" />
                      {connection.status === 'active' ? 'Choose repositories on GitHub' : 'Install on GitHub'}
                    </a>
                  </Button>
                )}
                {connection.status === 'active' && (
                  <Button size="sm" variant="outline" disabled={busy !== null} onClick={sync}>
                    {busy === 'sync' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <RefreshCw className="mr-2 h-4 w-4" aria-hidden="true" />}
                    Refresh repositories
                  </Button>
                )}
                <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => setConfirming(true)}>
                  <Unplug className="mr-2 h-4 w-4" aria-hidden="true" />
                  Disconnect
                </Button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex min-w-0 items-center gap-1 rounded-md border border-border bg-muted/40 py-1 pl-2 pr-1">
      <code className="min-w-0 flex-1 truncate text-xs" title={value}>{value}</code>
      <button
        type="button"
        aria-label={`Copy ${label}`}
        className="flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
        onClick={() => {
          void navigator.clipboard.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
      >
        {copied ? <Check className="h-3.5 w-3.5 text-emerald-600" aria-hidden="true" /> : <Copy className="h-3.5 w-3.5" aria-hidden="true" />}
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  );
}

function BitbucketCard({ connection }: { connection: CodeConnection }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [workspace, setWorkspace] = useState('');
  const [key, setKey] = useState('');
  const [secret, setSecret] = useState('');
  const [busy, setBusy] = useState<'connect' | 'sync' | 'disconnect' | null>(null);
  const [confirming, setConfirming] = useState(false);
  const consumerPage = workspace.trim()
    ? `https://bitbucket.org/${encodeURIComponent(workspace.trim().toLowerCase())}/workspace/settings/oauth-consumers/new`
    : 'https://bitbucket.org/account/workspaces/';

  const connect = async () => {
    setBusy('connect');
    try {
      const { data } = await integrationsApi.connectBitbucket({ workspace, key, secret });
      window.location.assign(data.url);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not start connecting Bitbucket.'));
      setBusy(null);
    }
  };

  const sync = async () => {
    setBusy('sync');
    try {
      const { data } = await integrationsApi.sync('bitbucket');
      queryClient.setQueryData(['integrations'], (old: any) => (old ? { ...old, data: data.connections } : old));
      await queryClient.invalidateQueries({ queryKey: ['repositories'] });
      toast.success('Repositories brought up to date.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not read the repositories from Bitbucket.'));
    } finally {
      setBusy(null);
    }
  };

  const disconnect = async () => {
    setBusy('disconnect');
    try {
      await integrationsApi.disconnect('bitbucket');
      await Promise.all(['integrations', 'repositories', 'projects'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
      toast.success('Bitbucket disconnected, and its webhook taken off the workspace.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not disconnect Bitbucket.'));
    } finally {
      setBusy(null);
      setConfirming(false);
    }
  };

  const connected = connection.status === 'active' || connection.status === 'broken';

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-3 space-y-0">
        <BitbucketMark />
        <CardTitle className="text-base">Bitbucket</CardTitle>
        {connection.status === 'active' && (
          <span className="ml-auto flex items-center gap-1 text-xs font-medium text-emerald-600">
            <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
            Connected
          </span>
        )}
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {!connection.available ? (
          <p role="alert" className="text-muted-foreground">
            The server has no TEAMUP_AUTH_SECRET, so Bitbucket&apos;s keys could not be kept safe. Set it, then come back.
          </p>
        ) : !connected ? (
          <>
            <p className="text-muted-foreground">An owner of the Bitbucket workspace does this, once.</p>
            <div className="space-y-2">
              <Label htmlFor="bbWorkspace">1. Your workspace</Label>
              <Input id="bbWorkspace" value={workspace} onChange={(e) => setWorkspace(e.target.value)} placeholder="cognecto, as in bitbucket.org/cognecto" />
            </div>
            <div className="space-y-2">
              <p className="font-medium">2. Make a consumer for TeamUp in Bitbucket</p>
              <ol className="list-decimal space-y-1 pl-5 text-muted-foreground">
                <li>
                  Open{' '}
                  <a href={consumerPage} target="_blank" rel="noopener noreferrer" className="font-medium text-primary hover:underline">
                    Workspace settings → OAuth consumers → Add consumer
                  </a>
                  .
                </li>
                <li>Name: TeamUp. Callback URL:</li>
              </ol>
              {connection.callbackUrl && <CopyField label="callback URL" value={connection.callbackUrl} />}
              <ol className="list-decimal space-y-1 pl-5 text-muted-foreground" start={3}>
                <li>Tick <span className="font-medium text-foreground">This is a private consumer</span>.</li>
                <li>
                  Under Permissions, tick:
                  <ul className="mt-1 list-disc space-y-0.5 pl-5">
                    {(connection.permissions ?? []).map((p) => (
                      <li key={p}>{p}</li>
                    ))}
                  </ul>
                </li>
                <li>Save, then open the consumer to see its Key and Secret.</li>
              </ol>
            </div>
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                void connect();
              }}
            >
              <p className="font-medium">3. Give TeamUp the key and secret</p>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="min-w-0 space-y-2">
                  <Label htmlFor="bbKey">Key</Label>
                  <Input id="bbKey" value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" />
                </div>
                <div className="min-w-0 space-y-2">
                  <Label htmlFor="bbSecret">Secret</Label>
                  <Input id="bbSecret" type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="off" />
                </div>
              </div>
              <Button type="submit" disabled={!workspace.trim() || !key.trim() || !secret.trim() || busy !== null}>
                {busy === 'connect' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <BitbucketMark />}
                <span className={busy === 'connect' ? '' : 'ml-2'}>Connect Bitbucket</span>
              </Button>
              <p className="text-xs text-muted-foreground">Bitbucket then asks you to sign in as an owner of the workspace and allow TeamUp.</p>
            </form>
          </>
        ) : (
          <>
            <p>
              Connected to <span className="font-medium">{connection.account}</span>, seeing{' '}
              {connection.repositories === 1 ? '1 repository' : `${connection.repositories} repositories`}.
            </p>
            {connection.lastError && (
              <p role="alert" className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-50 p-2 text-amber-950 dark:bg-amber-950/40 dark:text-amber-100">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                {connection.lastError}
              </p>
            )}
            {confirming ? (
              <div className="flex flex-wrap items-center gap-2">
                <span className="mr-auto">Disconnect Bitbucket? Links on tasks and linked accounts are removed.</span>
                <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => setConfirming(false)}>
                  Keep it
                </Button>
                <Button variant="destructive" size="sm" disabled={busy !== null} onClick={disconnect}>
                  {busy === 'disconnect' && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
                  Disconnect
                </Button>
              </div>
            ) : (
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="outline" disabled={busy !== null} onClick={sync}>
                  {busy === 'sync' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <RefreshCw className="mr-2 h-4 w-4" aria-hidden="true" />}
                  Refresh repositories
                </Button>
                <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => setConfirming(true)}>
                  <Unplug className="mr-2 h-4 w-4" aria-hidden="true" />
                  Disconnect
                </Button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
