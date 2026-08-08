import { describe, it, expect } from 'vitest';
import { apiErrorMessage, isClientError } from './errors';

function axiosError(status: number, data?: unknown) {
  return { isAxiosError: true, response: { status, data } } as any;
}

describe('apiErrorMessage', () => {
  it('shows the API message for a client error', () => {
    expect(apiErrorMessage(axiosError(400, { error: 'invalid status' })))
      .toBe('invalid status');
  });

  it('hides server-side detail behind the fallback', () => {
    // 5xx bodies wrap driver and constraint text; never show that to a user.
    expect(apiErrorMessage(axiosError(500, { error: 'pq: duplicate key value violates ...' }), 'Could not save'))
      .toBe('Could not save');
  });

  it('falls back when a client error carries no message', () => {
    expect(apiErrorMessage(axiosError(403, {}), 'Not allowed')).toBe('Not allowed');
  });

  it('explains an unreachable server rather than showing a generic failure', () => {
    const offline = { isAxiosError: true } as any;
    expect(apiErrorMessage(offline)).toMatch(/could not reach the server/i);
  });

  it('uses a plain Error message when the failure is not from axios', () => {
    expect(apiErrorMessage(new Error('boom'))).toBe('boom');
  });

  it('falls back for a thrown value with nothing useful in it', () => {
    expect(apiErrorMessage({}, 'Fallback')).toBe('Fallback');
  });
});

describe('isClientError', () => {
  it('is true for 4xx and false for 5xx', () => {
    expect(isClientError(axiosError(404))).toBe(true);
    expect(isClientError(axiosError(500))).toBe(false);
  });

  it('is false when there is no response', () => {
    expect(isClientError({ isAxiosError: true } as any)).toBe(false);
  });
});
