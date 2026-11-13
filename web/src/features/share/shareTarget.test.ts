import source from '../../../public/push-sw.js?raw';
import { describe, expect, it } from 'vitest';
import { SHARE_MAX_BYTES, SHARE_MAX_FILES } from './inbox';

// The worker's /share handler (public/push-sw.js) run as the browser runs it:
// the file evaluated against a stand-in for the worker's global scope, and a
// share posted to it.

const ORIGIN = 'https://teamup.example';

/** Response.redirect in a worker resolves against the worker's own address; Node's needs it whole. */
class WorkerResponse extends Response {
  static redirect(url: string, status?: number) {
    return Response.redirect(new URL(url, ORIGIN).href, status);
  }
}

function worker() {
  const store = new Map<string, Map<string, Response>>();
  const caches = {
    delete: async (name: string) => store.delete(name),
    open: async (name: string) => {
      if (!store.has(name)) store.set(name, new Map());
      const c = store.get(name)!;
      return {
        put: async (key: string, res: Response) => void c.set(key, res),
        match: async (key: string) => c.get(key),
      };
    },
  };
  const handlers: Record<string, (event: unknown) => void> = {};
  const self = { location: new URL(`${ORIGIN}/sw.js`), addEventListener: (t: string, fn: (e: unknown) => void) => (handlers[t] = fn) };
  new Function('self', 'caches', 'Response', source)(self, caches, WorkerResponse);

  async function share(form: FormData, referrer = '') {
    let answer: Promise<Response> | Response | undefined;
    handlers.fetch({
      request: { method: 'POST', url: `${ORIGIN}/share`, referrer, formData: async () => form },
      respondWith: (r: Promise<Response> | Response) => (answer = r),
    });
    return answer;
  }

  async function inbox() {
    const c = store.get('teamup-share-inbox');
    const meta = c?.get('/share-inbox/meta');
    if (!meta) return null;
    const m = await meta.clone().json();
    return { meta: m, keys: [...c!.keys()].filter((k) => k !== '/share-inbox/meta') };
  }

  return { share, inbox };
}

const file = (name: string, size: number) => new File([new Uint8Array(size)], name, { type: 'application/octet-stream' });

describe('the share target', () => {
  it('keeps what a share sheet sends, which comes with no referrer', async () => {
    const w = worker();
    const form = new FormData();
    form.set('text', 'Look at this');
    form.append('files', file('a.pdf', 10));
    const res = await w.share(form);
    expect(res?.status).toBe(303);
    expect(res?.headers.get('Location')).toBe(`${ORIGIN}/share`);
    const kept = await w.inbox();
    expect(kept?.meta.text).toBe('Look at this');
    expect(kept?.keys).toEqual(['/share-inbox/file/0']);
  });

  it('keeps nothing a page on another site posts to /share', async () => {
    const w = worker();
    const form = new FormData();
    form.set('text', 'Pay this invoice: https://evil.example/pay');
    form.append('files', file('invoice.exe', 10));
    const res = await w.share(form, 'https://evil.example/attack.html');
    expect(res?.status).toBe(303);
    expect(res?.headers.get('Location')).toBe(`${ORIGIN}/share`);
    expect(await w.inbox()).toBeNull();
  });

  it('still takes a share posted from TeamUp itself', async () => {
    const w = worker();
    const form = new FormData();
    form.set('text', 'From here');
    await w.share(form, `${ORIGIN}/chat`);
    expect((await w.inbox())?.meta.text).toBe('From here');
  });

  it(`keeps at most ${SHARE_MAX_FILES} files, and says how many were left out`, async () => {
    const w = worker();
    const form = new FormData();
    for (let i = 0; i < SHARE_MAX_FILES + 3; i++) form.append('files', file(`f${i}.txt`, 5));
    await w.share(form);
    const kept = await w.inbox();
    expect(kept?.keys).toHaveLength(SHARE_MAX_FILES);
    expect(kept?.meta.files).toHaveLength(SHARE_MAX_FILES);
    expect(kept?.meta.leftOut).toBe(3);
  });

  it('keeps no more than the byte limit, leaving out what would go over it', async () => {
    const w = worker();
    const form = new FormData();
    form.append('files', file('big.mov', SHARE_MAX_BYTES - 100));
    form.append('files', file('over.mov', 200));
    form.append('files', file('fits.txt', 50));
    await w.share(form);
    const kept = await w.inbox();
    expect(kept?.meta.files.map((f: { name: string }) => f.name)).toEqual(['big.mov', 'fits.txt']);
    expect(kept?.meta.leftOut).toBe(1);
  });
});
