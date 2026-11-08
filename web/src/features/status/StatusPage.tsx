import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, Loader2, RefreshCw, XCircle } from 'lucide-react';
import { format } from 'date-fns';
import { Button } from '@/shared/components/ui/button';
import { statusApi, type StatusReport } from '@/shared/api/client';
import { cn } from '@/shared/utils/cn';
import { BrandMark } from '@/features/branding/BrandMark';
import { durationText, hourText, overallText } from './statusText';

export const STATUS_QUERY_KEY = ['service-status'] as const;

/** The report, refreshed every half minute while somebody is looking. */
export function useServiceStatus(every = 30_000) {
  return useQuery({
    queryKey: STATUS_QUERY_KEY,
    queryFn: async () => (await statusApi.get()).data,
    refetchInterval: every,
    refetchOnWindowFocus: true,
    staleTime: 10_000,
    retry: 0,
  });
}

const BAR: Record<StatusReport['components'][number]['hours'][number]['status'], string> = {
  up: 'bg-emerald-500',
  partial: 'bg-amber-500',
  down: 'bg-red-500',
  none: 'bg-muted',
};

/**
 * Whether anything is broken, for anybody, signed in or not: the time you most
 * want to know is when signing in is what does not work.
 */
export function StatusPage() {
  const { data, error, isPending, isFetching, refetch, dataUpdatedAt } = useServiceStatus();
  const unreachable = !!error;
  const overall = unreachable ? 'unreachable' : data?.overall;

  return (
    <div className="min-h-screen bg-background">
      <div className="mx-auto max-w-3xl px-4 py-8 md:py-12">
        <header className="mb-8 flex items-center justify-between gap-4">
          <div className="min-w-0">
            <BrandMark />
            <h1 className="mt-2 text-sm font-medium text-muted-foreground">Service status</h1>
          </div>
          <Button asChild variant="outline" size="sm" className="shrink-0">
            <Link to="/chat">Open the app</Link>
          </Button>
        </header>

        {isPending ? (
          <div className="flex justify-center py-16">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-label="Loading" />
          </div>
        ) : (
          <>
            <section
              data-testid="status-overall"
              data-state={overall}
              className={cn(
                'flex items-start gap-3 rounded-lg border p-4',
                overall === 'operational' && 'border-emerald-500/40 bg-emerald-500/10',
                overall === 'degraded' && 'border-amber-500/40 bg-amber-500/10',
                (overall === 'outage' || overall === 'unreachable') && 'border-red-500/40 bg-red-500/10'
              )}
              role="status"
            >
              {overall === 'operational' ? (
                <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
              ) : overall === 'degraded' ? (
                <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
              ) : (
                <XCircle className="mt-0.5 h-5 w-5 shrink-0 text-red-600 dark:text-red-400" aria-hidden="true" />
              )}
              <div className="min-w-0 flex-1">
                <p className="font-semibold">{overallText(overall)}</p>
                <p className="mt-0.5 text-sm text-muted-foreground">
                  {unreachable
                    ? 'The server did not answer. It may be down or restarting. This page keeps trying.'
                    : `Checked at ${format(new Date(data!.checkedAt), 'HH:mm:ss')}. Updates every 30 seconds.`}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="shrink-0"
                onClick={() => refetch()}
                disabled={isFetching}
                aria-label="Check again"
              >
                <RefreshCw className={cn('h-4 w-4', isFetching && 'animate-spin')} />
              </Button>
            </section>

            {data && (
              <>
                <section className="mt-8" aria-labelledby="services-heading">
                  <div className="mb-3 flex items-baseline justify-between gap-2">
                    <h2 id="services-heading" className="font-semibold">
                      Services
                    </h2>
                    <span className="text-xs text-muted-foreground">Past 24 hours</span>
                  </div>
                  <ul className="divide-y divide-border rounded-lg border border-border bg-card">
                    {data.components.map((c) => (
                      <li key={c.key} className="p-4" data-testid={`status-${c.key}`} data-state={c.status}>
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0">
                            <p className="font-medium">{c.name}</p>
                            <p className="text-sm text-muted-foreground">{c.description}</p>
                          </div>
                          <span
                            className={cn(
                              'shrink-0 rounded-full px-2 py-0.5 text-xs font-medium',
                              c.status === 'operational'
                                ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
                                : 'bg-red-500/15 text-red-700 dark:text-red-400'
                            )}
                          >
                            {c.status === 'operational' ? 'Working' : 'Down'}
                          </span>
                        </div>
                        {data.historyAvailable && (
                          <>
                            <div className="mt-3 flex h-7 gap-[2px]" aria-hidden="true">
                              {c.hours.map((h) => (
                                <span
                                  key={h.start}
                                  title={hourText(h)}
                                  className={cn('min-w-0 flex-1 rounded-sm', BAR[h.status])}
                                />
                              ))}
                            </div>
                            <div className="mt-1 flex justify-between text-xs text-muted-foreground">
                              <span>24 hours ago</span>
                              <span>
                                {c.uptime === undefined ? 'No record yet' : `${formatUptime(c.uptime)} up`}
                              </span>
                              <span>Now</span>
                            </div>
                          </>
                        )}
                      </li>
                    ))}
                  </ul>
                  {data.historyAvailable && (
                    <ul className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-label="Key">
                      <li className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-sm bg-emerald-500" />Working</li>
                      <li className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-sm bg-amber-500" />Down for part of the hour</li>
                      <li className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-sm bg-red-500" />Down</li>
                      <li className="flex items-center gap-1.5"><span className="h-2.5 w-2.5 rounded-sm bg-muted" />No record</li>
                    </ul>
                  )}
                </section>

                <section className="mt-8" aria-labelledby="incidents-heading">
                  <h2 id="incidents-heading" className="mb-3 font-semibold">
                    Outages
                  </h2>
                  {!data.historyAvailable ? (
                    <p className="text-sm text-muted-foreground">
                      The history cannot be read right now. What you see above is how things are this moment.
                    </p>
                  ) : (
                    <Incidents report={data} />
                  )}
                </section>
              </>
            )}

            <p className="mt-10 text-center text-xs text-muted-foreground">
              Last updated {dataUpdatedAt ? format(new Date(dataUpdatedAt), 'HH:mm:ss') : 'never'}
            </p>
          </>
        )}
      </div>
    </div>
  );
}

function formatUptime(u: number): string {
  if (u >= 100) return '100%';
  return `${Math.floor(u * 100) / 100}%`;
}

function Incidents({ report }: { report: StatusReport }) {
  const all = report.components
    .flatMap((c) => c.incidents.map((i) => ({ ...i, name: c.name, key: c.key })))
    .sort((a, b) => b.start.localeCompare(a.start));
  if (all.length === 0) {
    return <p className="text-sm text-muted-foreground">No outages in the past 24 hours.</p>;
  }
  return (
    <ul className="space-y-2" data-testid="status-incidents">
      {all.map((i) => (
        <li
          key={`${i.key}-${i.start}`}
          className={cn(
            'rounded-lg border p-3 text-sm',
            i.end ? 'border-border bg-card' : 'border-red-500/40 bg-red-500/10'
          )}
        >
          <p className="font-medium">
            {i.name} {i.end ? `was down for ${durationText(i.minutes)}` : 'is down'}
          </p>
          <p className="text-muted-foreground">
            {i.end
              ? `${format(new Date(i.start), 'd MMM, HH:mm')} to ${format(new Date(i.end), 'HH:mm')}`
              : `Since ${format(new Date(i.start), 'd MMM, HH:mm')}, ${durationText(i.minutes)} so far`}
          </p>
        </li>
      ))}
    </ul>
  );
}
