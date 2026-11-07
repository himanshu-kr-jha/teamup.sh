import { describe, expect, it, vi } from 'vitest';
import { focusManager, QueryClient, QueryObserver } from '@tanstack/react-query';
import { liveTaskQuery, refreshTasksFor } from './liveQuery';

// The app's real defaults, which are what made the board stale.
const appDefaults = {
  defaultOptions: { queries: { staleTime: 5 * 60 * 1000, refetchOnWindowFocus: false } },
};

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('liveTaskQuery', () => {
  it('refetches when somebody comes back to the window, despite the five minute default', async () => {
    const client = new QueryClient(appDefaults);
    client.mount();
    const queryFn = vi.fn().mockResolvedValue('tasks');
    const observer = new QueryObserver(client, { queryKey: ['tasks'], queryFn, ...liveTaskQuery });
    const unsubscribe = observer.subscribe(() => undefined);
    await settle();
    expect(queryFn).toHaveBeenCalledTimes(1);

    focusManager.setFocused(false);
    focusManager.setFocused(true);
    await settle();
    expect(queryFn).toHaveBeenCalledTimes(2);

    unsubscribe();
    focusManager.setFocused(undefined);
    client.unmount();
  });
});

describe('refreshTasksFor', () => {
  it('marks the board, the task and the dashboard stale for a notification about a task', () => {
    const client = new QueryClient(appDefaults);
    ['tasks', 'dashboard'].forEach((key) => client.setQueryData([key], 'x'));
    client.setQueryData(['task', 't1'], 'x');
    client.setQueryData(['task', 't2'], 'x');

    refreshTasksFor(client, { data: { taskId: 't1' } });

    const stale = (key: unknown[]) => client.getQueryState(key)?.isInvalidated;
    expect(stale(['tasks'])).toBe(true);
    expect(stale(['dashboard'])).toBe(true);
    expect(stale(['task', 't1'])).toBe(true);
    expect(stale(['task', 't2'])).toBe(false);
  });

  it('leaves everything alone for a notification about anything else', () => {
    const client = new QueryClient(appDefaults);
    client.setQueryData(['tasks'], 'x');
    refreshTasksFor(client, { data: {} });
    refreshTasksFor(client, {});
    refreshTasksFor(client, undefined);
    expect(client.getQueryState(['tasks'])?.isInvalidated).toBe(false);
  });
});
