import { describe, expect, it } from 'vitest';
import { addPrompt, classifyJoinError, removePrompt, removePromptsForCall, retryAfterSeconds, secondsLeft, type KnockPrompt } from './knock';

const axiosError = (status: number, data: unknown = {}, headers: Record<string, string> = {}) => ({
  isAxiosError: true,
  response: { status, data, headers },
});

describe('classifyJoinError', () => {
  it('recognises a private call that can be knocked on', () => {
    expect(classifyJoinError(axiosError(403, { code: 'knock_required', canKnock: true, error: 'x' })).kind).toBe('knock');
  });

  it('says a plain refusal is one', () => {
    expect(classifyJoinError(axiosError(403, { error: 'no' })).kind).toBe('other');
  });

  it('knows an ended call, and a missing one', () => {
    expect(classifyJoinError(axiosError(409, { error: 'over' })).kind).toBe('over');
    expect(classifyJoinError(axiosError(404, { error: 'gone' })).kind).toBe('missing');
  });

  it('treats a dead network as something to retry', () => {
    const failure = classifyJoinError({ isAxiosError: true });
    expect(failure.kind).toBe('other');
    expect(failure.message).toMatch(/server/i);
  });
});

describe('retryAfterSeconds', () => {
  it('reads the body, then the header, then assumes the minute', () => {
    expect(retryAfterSeconds(axiosError(429, { retryAfterSeconds: 12 }))).toBe(12);
    expect(retryAfterSeconds(axiosError(429, {}, { 'retry-after': '40' }))).toBe(40);
    expect(retryAfterSeconds(axiosError(429))).toBe(60);
  });
});

describe('secondsLeft', () => {
  it('counts down in whole seconds and stops at zero', () => {
    expect(secondsLeft(10_000, 9_001)).toBe(1);
    expect(secondsLeft(10_000, 10_000)).toBe(0);
    expect(secondsLeft(10_000, 20_000)).toBe(0);
  });
});

describe('prompts', () => {
  const prompt = (requestId: string, callId = 'c1'): KnockPrompt => ({
    callId,
    requestId,
    userId: 'u',
    userName: 'Ann',
    connected: true,
  });

  it('adds a request once, however often it is heard about', () => {
    const one = addPrompt([], prompt('r1'));
    expect(addPrompt(one, prompt('r1'))).toBe(one);
    expect(addPrompt(one, prompt('r2'))).toHaveLength(2);
  });

  it('clears one, and all of a call that ended', () => {
    const all = [prompt('r1'), prompt('r2'), prompt('r3', 'c2')];
    expect(removePrompt(all, 'r2').map((p) => p.requestId)).toEqual(['r1', 'r3']);
    expect(removePromptsForCall(all, 'c1').map((p) => p.requestId)).toEqual(['r3']);
  });
});
