import { Link } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import { useServiceStatus } from './StatusPage';
import { downNames } from './statusText';

/**
 * A line across the top of the app while something is not working, so a call
 * that will not connect or a file that will not open is explained before
 * anybody reports it. Checks once a minute; the server reuses its answer, so
 * many people looking costs little.
 */
export function OutageBanner() {
  const { data } = useServiceStatus(60_000);
  if (!data || data.overall === 'operational') return null;
  const names = downNames(data);
  return (
    <div
      role="status"
      data-testid="outage-banner"
      className="flex shrink-0 items-center gap-2 border-b border-amber-500/40 bg-amber-500/15 px-4 py-2 text-sm"
    >
      <AlertTriangle className="h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
      <span className="min-w-0 flex-1">
        {names.length ? `Not working right now: ${names.join(', ')}.` : 'Some things are not working right now.'}
      </span>
      <Link to="/status" className="shrink-0 font-medium underline underline-offset-2">
        See status
      </Link>
    </div>
  );
}
