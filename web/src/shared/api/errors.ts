import { AxiosError } from 'axios';

/**
 * Turns whatever a request threw into something worth showing a user.
 *
 * Errors used to reach only `console.error`, so a failed save looked exactly
 * like a successful one. The API answers client errors with a readable
 * `error` field and server errors with internal detail, so 5xx responses are
 * replaced with the caller's fallback rather than echoed back.
 */
export function apiErrorMessage(err: unknown, fallback = 'Something went wrong. Please try again.'): string {
  const axiosErr = err as AxiosError<{ error?: string; missingSteps?: number[] }>;

  if (!axiosErr?.isAxiosError) {
    return err instanceof Error && err.message ? err.message : fallback;
  }

  // No response at all: the server is unreachable or the request was aborted.
  if (!axiosErr.response) {
    return 'Could not reach the server. Check your connection and try again.';
  }

  const status = axiosErr.response.status;
  const message = axiosErr.response.data?.error;

  if (status >= 500 || !message) {
    return fallback;
  }
  return message;
}

/** Field-level validation errors keyed by field name, when the API sends them. */
export function apiFieldErrors(err: unknown): Record<string, string> {
  const axiosErr = err as AxiosError<{ fields?: Record<string, string> }>;
  return axiosErr?.response?.data?.fields ?? {};
}

/** True when the failure is the caller's fault and worth showing inline on a form. */
export function isClientError(err: unknown): boolean {
  const axiosErr = err as AxiosError;
  const status = axiosErr?.response?.status;
  return typeof status === 'number' && status >= 400 && status < 500;
}
