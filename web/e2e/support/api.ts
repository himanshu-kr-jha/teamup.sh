import { request, type APIRequestContext } from '@playwright/test';

export const BASE_URL = process.env.E2E_BASE_URL ?? 'http://localhost:8080';

/** A signed-in API client. Cookies are kept, exactly as a browser keeps them. */
export class Api {
  private constructor(
    readonly ctx: APIRequestContext,
    readonly userId: string
  ) {}

  static async signIn(email: string, password: string): Promise<Api> {
    const ctx = await request.newContext({ baseURL: BASE_URL });
    const res = await ctx.post('/api/auth/login', { data: { email, password } });
    if (!res.ok()) throw new Error(`sign-in as ${email} failed: ${res.status()} ${await res.text()}`);
    const body = await res.json();
    return new Api(ctx, body.user.id);
  }

  async call<T = any>(method: 'get' | 'post' | 'patch' | 'delete', path: string, data?: unknown, expected = [200, 201, 204]): Promise<T> {
    const res = await this.ctx[method](`/api${path}`, data === undefined ? {} : { data });
    if (!expected.includes(res.status())) {
      throw new Error(`${method.toUpperCase()} ${path} -> ${res.status()} ${await res.text()}`);
    }
    const text = await res.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  get = <T = any>(path: string) => this.call<T>('get', path);
  post = <T = any>(path: string, data?: unknown) => this.call<T>('post', path, data ?? {});
  patch = <T = any>(path: string, data: unknown) => this.call<T>('patch', path, data);
  del = <T = any>(path: string) => this.call<T>('delete', path);

  /** Status only, for asserting what somebody is refused. */
  async status(method: 'get' | 'post' | 'patch' | 'put' | 'delete', path: string, data?: unknown): Promise<number> {
    const res = await this.ctx[method](`/api${path}`, data === undefined ? {} : { data });
    return res.status();
  }

  dispose() {
    return this.ctx.dispose();
  }
}
