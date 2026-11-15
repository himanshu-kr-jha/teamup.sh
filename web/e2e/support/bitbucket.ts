import { createHmac } from 'node:crypto';
import { createServer, type IncomingMessage, type Server } from 'node:http';
import type { APIRequestContext, Page } from '@playwright/test';
import { expect } from './fixtures';

const PORT = Number(process.env.E2E_BITBUCKET_PORT ?? 18093);
const BASE = `http://127.0.0.1:${PORT}`;

export const ACCOUNTS: Record<string, { uuid: string; account_id: string; nickname: string; display_name: string }> = {
  'owner-code': { uuid: '{owner}', account_id: '557058:owner', nickname: 'olivia', display_name: 'Olivia Owner' },
  'alice-code': { uuid: '{alice}', account_id: '557058:alice', nickname: 'alice-bb', display_name: 'Alice on Bitbucket' },
  'sam-code': { uuid: '{sam}', account_id: '557058:sam', nickname: 'sam-bb', display_name: 'Sam on Bitbucket' },
};

/**
 * Bitbucket, as far as TeamUp and a browser can tell: the sign-in page, the
 * consumer's token endpoint, the workspace and its webhook, and people. The
 * server under test is pointed here by TEAMUP_BITBUCKET_WEB_URL and _API_URL.
 */
export async function fakeBitbucket() {
  const hooks: Array<{ url: string; secret: string; uuid: string }> = [];
  // Branches made, as "cognecto/cortex pho-1-fix from hash-master".
  const branches: string[] = [];
  let nextUser = 'owner-code';
  const read = (req: IncomingMessage) =>
    new Promise<string>((done) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => done(body));
    });
  const server: Server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', BASE);
    const body = await read(req);
    const json = (value: unknown, status = 200) => res.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(value));
    const who = (req.headers.authorization ?? '').replace('Bearer access-', '');
    if (url.pathname === '/site/oauth2/authorize') {
      // Bitbucket sends people to the callback set on the consumer, the address TeamUp showed.
      return res
        .writeHead(302, { Location: `http://localhost:${process.env.E2E_PORT}/api/integrations/bitbucket/callback?code=${nextUser}&state=${encodeURIComponent(url.searchParams.get('state') ?? '')}` })
        .end();
    }
    if (url.pathname === '/site/oauth2/access_token') {
      const auth = Buffer.from((req.headers.authorization ?? '').replace('Basic ', ''), 'base64').toString();
      if (auth !== 'e2e-key:e2e-secret') return json({ error: 'invalid_client' }, 401);
      const form = new URLSearchParams(body);
      const code = form.get('grant_type') === 'refresh_token' ? 'owner-code' : form.get('code');
      return json({ access_token: `access-${code}`, refresh_token: `refresh-${Date.now()}`, expires_in: 3600 });
    }
    if (url.pathname === '/2.0/user/workspaces/cognecto/permission') return json({ permission: who === 'owner-code' ? 'owner' : 'member' });
    if (url.pathname === '/2.0/workspaces/cognecto/hooks' && req.method === 'POST') {
      const hook = { ...JSON.parse(body), uuid: '{hook}' };
      hooks.push(hook);
      return json(hook);
    }
    if (url.pathname.startsWith('/2.0/workspaces/cognecto/hooks/') && req.method === 'DELETE') {
      hooks.length = 0;
      return res.writeHead(204).end();
    }
    if (url.pathname === '/2.0/repositories/cognecto') {
      return json({ values: [{ uuid: '{cortex}', full_name: 'cognecto/cortex', is_private: true, links: { html: { href: 'https://bitbucket.org/cognecto/cortex' } } }] });
    }
    // Giving somebody a repository: only workspace members can be given one.
    if (url.pathname.startsWith('/2.0/repositories/cognecto/cortex/permissions-config/users/')) {
      if (req.method === 'DELETE') return res.writeHead(204).end();
      return json({ permission: JSON.parse(body || '{}').permission });
    }
    if (url.pathname.startsWith('/2.0/workspaces/cognecto/permissions/repositories/')) return json({ values: [] });
    if (url.pathname === '/2.0/repositories/cognecto/cortex') return json({ full_name: 'cognecto/cortex', mainbranch: { name: 'master' } });
    const branchPath = '/2.0/repositories/cognecto/cortex/refs/branches';
    if (url.pathname === branchPath && req.method === 'POST') {
      const made = JSON.parse(body) as { name: string; target: { hash: string } };
      branches.push(`cognecto/cortex ${made.name} from ${made.target.hash}`);
      return json({ name: made.name }, 201);
    }
    if (url.pathname.startsWith(branchPath + '/') && req.method === 'GET') {
      const name = decodeURIComponent(url.pathname.slice(branchPath.length + 1));
      if (name === 'master' || branches.some((b) => b.startsWith(`cognecto/cortex ${name} `))) {
        return json({ name, target: { hash: `hash-${name}` } });
      }
      return res.writeHead(404).end();
    }
    if (url.pathname === '/2.0/user' && ACCOUNTS[who]) return json(ACCOUNTS[who]);
    if (url.pathname === '/2.0/user/emails') return json({ values: [] });
    res.writeHead(404).end();
  });
  await new Promise<void>((done) => server.listen(PORT, '127.0.0.1', done));
  return {
    signInAs: (code: string) => (nextUser = code),
    hook: () => hooks[0],
    branches,
    close: () => new Promise((done) => server.close(done)),
  };
}


/** The workspace owner connects Bitbucket from Organization, Integrations. */
export async function connectBitbucket(page: Page, bitbucket: Awaited<ReturnType<typeof fakeBitbucket>>) {
  await page.goto('/organization');
  await page.getByRole('tab', { name: 'Integrations' }).click();
  await page.getByLabel('1. Your workspace').fill('cognecto');
  await page.getByLabel('Key').fill('e2e-key');
  await page.getByLabel('Secret').fill('e2e-secret');
  bitbucket.signInAs('owner-code');
  await page.getByRole('button', { name: 'Connect Bitbucket' }).click();
  await expect(page.getByText('Bitbucket is connected.', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
}

/** Somebody links their own Bitbucket account under Settings. */
export async function linkBitbucket(page: Page, bitbucket: Awaited<ReturnType<typeof fakeBitbucket>>, code: keyof typeof ACCOUNTS) {
  bitbucket.signInAs(code);
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Link Bitbucket' }).click();
  await expect(page.getByText(`Linked as @${ACCOUNTS[code].nickname}`)).toBeVisible({ timeout: 20_000 });
}

/** Sends one signed event to TeamUp's webhook, as Bitbucket does. */
export async function sendBitbucket(request: APIRequestContext, hook: { url: string; secret: string; uuid: string }, event: string, payload: unknown) {
  const raw = JSON.stringify(payload);
  const res = await request.post(hook.url.replace(/^https?:\/\/[^/]+/, ''), {
    data: raw,
    headers: {
      'Content-Type': 'application/json',
      'X-Event-Key': event,
      'X-Hook-UUID': hook.uuid,
      'X-Request-UUID': `${event}-${Date.now()}-${Math.random()}`,
      'X-Hub-Signature': 'sha256=' + createHmac('sha256', hook.secret).update(raw).digest('hex'),
    },
  });
  expect(res.status(), `${event} accepted`).toBe(204);
}
