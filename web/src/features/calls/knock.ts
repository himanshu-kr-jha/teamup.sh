import { apiErrorMessage } from '@/shared/api/errors';

/** Why a token was refused, as far as what to do next is concerned. */
export interface JoinFailure {
  kind: 'knock' | 'over' | 'missing' | 'other';
  message: string;
}

interface HttpError {
  isAxiosError?: boolean;
  response?: {
    status?: number;
    data?: { code?: string; canKnock?: boolean; error?: string; retryAfterSeconds?: number };
    headers?: Record<string, string | undefined>;
  };
}

export function classifyJoinError(err: unknown): JoinFailure {
  const { response } = (err ?? {}) as HttpError;
  const message = apiErrorMessage(err, 'Could not join this call.');
  if (response?.status === 403 && response.data?.code === 'knock_required') return { kind: 'knock', message };
  if (response?.status === 409) return { kind: 'over', message };
  if (response?.status === 404) return { kind: 'missing', message };
  return { kind: 'other', message };
}

export const KNOCK_COOLDOWN_SECONDS = 60;

/** How long until somebody turned away may ask again. */
export function retryAfterSeconds(err: unknown): number {
  const { response } = (err ?? {}) as HttpError;
  const fromBody = response?.data?.retryAfterSeconds;
  if (typeof fromBody === 'number' && fromBody > 0) return fromBody;
  const fromHeader = Number(response?.headers?.['retry-after']);
  if (Number.isFinite(fromHeader) && fromHeader > 0) return fromHeader;
  return KNOCK_COOLDOWN_SECONDS;
}

export function secondsLeft(until: number, now: number): number {
  return Math.max(0, Math.ceil((until - now) / 1000));
}

/** Somebody at the door, as a member of the call sees it. */
export interface KnockPrompt {
  callId: string;
  requestId: string;
  userId: string;
  userName: string;
  /** False when nobody was in the call, so this is reaching you outside it. */
  connected: boolean;
}

export function addPrompt(prompts: KnockPrompt[], prompt: KnockPrompt): KnockPrompt[] {
  return prompts.some((p) => p.requestId === prompt.requestId) ? prompts : [...prompts, prompt];
}

export function removePrompt(prompts: KnockPrompt[], requestId: string): KnockPrompt[] {
  return prompts.filter((p) => p.requestId !== requestId);
}

export function removePromptsForCall(prompts: KnockPrompt[], callId: string): KnockPrompt[] {
  return prompts.filter((p) => p.callId !== callId);
}
