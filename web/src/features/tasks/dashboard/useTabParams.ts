import { useCallback } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { TabId } from './logic';

/**
 * The tasks page's state lives in the address, so a reload, a deep link or the
 * way back from a task lands on the same view.
 */
export function useTabParams() {
  const [params, setParams] = useSearchParams();

  const update = useCallback(
    (changes: Record<string, string | null>) => {
      setParams((current) => {
        const next = new URLSearchParams(current);
        Object.entries(changes).forEach(([key, value]) => {
          if (value) next.set(key, value);
          else next.delete(key);
        });
        return next;
      });
    },
    [setParams]
  );

  const team = params.get('team') ?? undefined;

  /** A search string for a link within this page. Only the team filter carries across tabs. */
  const linkTo = useCallback(
    (tab: TabId, extra: Record<string, string> = {}) => {
      const next = new URLSearchParams({ tab, ...extra });
      if (team) next.set('team', team);
      return `?${next.toString()}`;
    },
    [team]
  );

  return {
    tab: params.get('tab'),
    team,
    person: params.get('person'),
    scrum: params.get('scrum'),
    from: params.get('from'),
    to: params.get('to'),
    update,
    linkTo,
    selectTab: useCallback(
      (tab: string) => update({ tab, person: null, scrum: null, from: null, to: null }),
      [update]
    ),
  };
}
