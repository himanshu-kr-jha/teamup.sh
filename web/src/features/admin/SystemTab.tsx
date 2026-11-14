import { useQuery } from '@tanstack/react-query';
import { format, formatDistanceToNowStrict } from 'date-fns';
import { AlertTriangle, CheckCircle2, Cpu, Database, HardDrive, Loader2, MemoryStick, PhoneCall, Users, Activity, XCircle } from 'lucide-react';
import type { ReactNode } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { systemApi, type SystemPoint, type SystemReport } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { cn } from '@/shared/utils/cn';

// Where a meter turns amber and red, the same limits the server warns at.
const LIMITS = { cpu: [80, 95], memory: [85, 95], disk: [80, 90] } as const;

export function bytes(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

export function uptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d} day${d === 1 ? '' : 's'} ${h} h`;
  if (h > 0) return `${h} h ${m} min`;
  return `${m} min`;
}

function tone(value: number | null | undefined, [warn, alarm]: readonly [number, number]) {
  if (value == null) return 'none';
  if (value >= alarm) return 'alarm';
  if (value >= warn) return 'warn';
  return 'ok';
}

const BAR = { ok: 'bg-emerald-500', warn: 'bg-amber-500', alarm: 'bg-red-500', none: 'bg-muted-foreground/30' };

function Meter({ label, value, detail, limits }: { label: string; value: number | null; detail: string; limits: readonly [number, number] }) {
  const t = tone(value, limits);
  return (
    <div data-testid={`meter-${label.toLowerCase()}`}>
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <span className="font-medium">{label}</span>
        <span className={cn('tabular-nums', t === 'alarm' && 'font-semibold text-red-600 dark:text-red-400', t === 'warn' && 'font-semibold text-amber-600 dark:text-amber-400')}>
          {value == null ? 'not available' : `${Math.round(value)}%`}
        </span>
      </div>
      <div
        className="mt-1.5 h-2 overflow-hidden rounded-full bg-muted"
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={value == null ? undefined : Math.round(value)}
      >
        <div className={cn('h-full rounded-full transition-[width] duration-500', BAR[t])} style={{ width: `${value ?? 0}%` }} />
      </div>
      <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
    </div>
  );
}

function Figure({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 truncate text-xl font-semibold tabular-nums">{value}</dd>
      {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function Section({ title, icon, children }: { title: string; icon: ReactNode; children: ReactNode }) {
  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          {icon}
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

/**
 * A small line chart of the last 24 hours. A step nothing was recorded in is a
 * gap, not a fall to zero, so a server that was off does not look idle.
 */
function Chart({ points, pick, label, unit, max, stepMinutes }: {
  points: SystemPoint[];
  pick: (p: SystemPoint) => number | null;
  label: string;
  unit: string;
  max?: number;
  stepMinutes: number;
}) {
  const W = 300;
  const H = 64;
  const end = Date.now();
  const start = end - 24 * 3600 * 1000;
  const values = points.map(pick);
  const top = Math.max(max ?? 0, ...values.map((v) => v ?? 0), 1);
  const x = (at: string) => ((new Date(at).getTime() - start) / (end - start)) * W;
  const y = (v: number) => H - (v / top) * (H - 4) - 2;

  // Runs of consecutive steps become one line each.
  const runs: string[] = [];
  let run: string[] = [];
  let prevAt = 0;
  points.forEach((p, i) => {
    const v = values[i];
    const at = new Date(p.at).getTime();
    if (v == null || (run.length && at - prevAt > stepMinutes * 60 * 1000 * 1.5)) {
      if (run.length) runs.push(run.join(' '));
      run = [];
    }
    if (v != null) run.push(`${x(p.at).toFixed(1)},${y(v).toFixed(1)}`);
    prevAt = at;
  });
  if (run.length) runs.push(run.join(' '));

  const recorded = values.filter((v): v is number => v != null);
  const peak = recorded.length ? Math.max(...recorded) : null;
  return (
    <figure className="min-w-0">
      <figcaption className="flex items-baseline justify-between text-sm">
        <span className="font-medium">{label}</span>
        <span className="text-xs text-muted-foreground tabular-nums">
          {peak == null ? 'nothing recorded yet' : `highest ${Math.round(peak * 10) / 10}${unit}`}
        </span>
      </figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="mt-1 h-16 w-full rounded bg-muted/40" role="img" aria-label={`${label} over the last 24 hours`}>
        {runs.map((r, i) =>
          r.includes(' ') ? (
            <polyline key={i} points={r} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" className="text-primary" />
          ) : (
            <circle key={i} cx={r.split(',')[0]} cy={r.split(',')[1]} r="1.5" className="fill-primary" />
          )
        )}
      </svg>
      <div className="mt-0.5 flex justify-between text-[10px] text-muted-foreground">
        <span>24 h ago</span>
        <span>now</span>
      </div>
    </figure>
  );
}

function Warnings({ report }: { report: SystemReport }) {
  if (report.warnings.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3 text-sm" role="status" data-testid="system-all-clear">
        <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
        Nothing is near a limit.
      </div>
    );
  }
  return (
    <ul className="space-y-2" aria-label="Warnings" data-testid="system-warnings">
      {report.warnings.map((w) => (
        <li
          key={w.text}
          className={cn(
            'flex items-start gap-2 rounded-lg border p-3 text-sm',
            w.level === 'alarm' ? 'border-red-500/40 bg-red-500/10' : 'border-amber-500/40 bg-amber-500/10'
          )}
        >
          {w.level === 'alarm' ? (
            <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-red-600 dark:text-red-400" aria-hidden="true" />
          ) : (
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
          )}
          {w.text}
        </li>
      ))}
    </ul>
  );
}

function backupText(b: SystemReport['backup']): { text: string; hint: string } {
  if (!b) return { text: 'None yet', hint: 'Backups run every night.' };
  const when = new Date(b.finishedAt);
  const where = b.uploaded == null ? 'kept on this server' : b.uploaded ? 'copied to the files bucket' : 'not copied to the files bucket';
  return {
    text: `${formatDistanceToNowStrict(when)} ago`,
    hint: `${format(when, 'd MMM, HH:mm')}, ${bytes(b.sizeBytes)}, ${where}`,
  };
}

/**
 * How hard the server is working, for whoever may change the organization's
 * settings. Updates every five seconds while it is on screen; the charts every
 * minute.
 */
export function SystemTab() {
  const { data, error, isPending } = useQuery({
    queryKey: ['system'],
    queryFn: () => systemApi.get(),
    refetchInterval: 5_000,
  });
  const { data: history } = useQuery({
    queryKey: ['system-history'],
    queryFn: () => systemApi.history(),
    refetchInterval: 60_000,
  });

  if (isPending) {
    return (
      <div className="flex justify-center py-16">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-label="Loading" />
      </div>
    );
  }
  if (error || !data) {
    return <p className="text-sm text-destructive">{apiErrorMessage(error, 'Could not read how the server is doing.')}</p>;
  }
  const r = data.data;
  const s = r.server;
  const memUsed = s.memoryTotal ? (100 * (s.memoryTotal - s.memoryAvailable)) / s.memoryTotal : null;
  const diskUsed = s.diskTotal ? (100 * (s.diskTotal - s.diskFree)) / s.diskTotal : null;
  const points = history?.data.points ?? [];
  const step = history?.data.stepMinutes ?? 5;
  const backup = backupText(r.backup);

  return (
    <div className="mx-auto max-w-5xl space-y-4" data-testid="system-tab">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">System</h2>
          <p className="text-sm text-muted-foreground">How hard this server is working. Updates every 5 seconds.</p>
        </div>
        <p className="text-xs text-muted-foreground tabular-nums">Checked at {format(new Date(r.checkedAt), 'HH:mm:ss')}</p>
      </div>

      <Warnings report={r} />

      <div className="grid gap-4 md:grid-cols-2">
        <Section title="Server" icon={<Cpu className="h-4 w-4" aria-hidden="true" />}>
          {s.procAvailable ? (
            <div className="space-y-4">
              <Meter
                label="Processors"
                value={s.cpuPercent}
                limits={LIMITS.cpu}
                detail={`${s.cores} cores${s.load1 != null ? `, ${s.load1.toFixed(2)} waiting on average` : ''}${
                  s.stealPercent != null && s.stealPercent >= 1 ? `, ${Math.round(s.stealPercent)}% held back by the cloud provider` : ''
                }`}
              />
              <Meter label="Memory" value={memUsed} limits={LIMITS.memory} detail={`${bytes(s.memoryTotal - s.memoryAvailable)} of ${bytes(s.memoryTotal)} in use`} />
              <Meter label="Disk" value={diskUsed} limits={LIMITS.disk} detail={`${bytes(s.diskFree)} free of ${bytes(s.diskTotal)}`} />
              {s.uptimeSeconds != null && <p className="text-xs text-muted-foreground">Running for {uptime(s.uptimeSeconds)}.</p>}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">This server does not let TeamUp read its processors and memory.</p>
          )}
        </Section>

        <Section title="People" icon={<Users className="h-4 w-4" aria-hidden="true" />}>
          <dl className="grid grid-cols-3 gap-4">
            <Figure label="Online now" value={r.people.online} />
            <Figure label="Active in 24 h" value={r.people.activeLastDay} />
            <Figure label="Members" value={r.people.members} />
          </dl>
          <p className="mt-3 text-xs text-muted-foreground">{s.connections} open connections to this server, counting every tab and phone.</p>
        </Section>

        <Section title="Calls" icon={<PhoneCall className="h-4 w-4" aria-hidden="true" />}>
          <dl className="grid grid-cols-3 gap-4">
            <Figure label="In progress" value={r.calls.active} />
            <Figure label="People in calls" value={r.calls.peopleInCalls} />
            <Figure label="Biggest call" value={r.calls.busiest ? r.calls.busiest.people : '–'} hint={r.calls.busiest?.title} />
          </dl>
          <p className="mt-3 text-xs text-muted-foreground">{r.calls.note}</p>
        </Section>

        <Section title="App" icon={<Activity className="h-4 w-4" aria-hidden="true" />}>
          <dl className="grid grid-cols-3 gap-4">
            <Figure label="Requests a minute" value={r.app.requestsPerMinute == null ? '–' : Math.round(r.app.requestsPerMinute)} />
            <Figure
              label="Answer time"
              value={r.app.p50ms == null ? '–' : `${Math.round(r.app.p50ms)} ms`}
              hint={r.app.p95ms == null ? undefined : `95 in 100 within ${Math.round(r.app.p95ms)}\u00a0ms`}
            />
            <Figure label="Errors in the last hour" value={r.app.errorsLastHour ?? '–'} />
          </dl>
          {r.app.requestsPerMinute == null && <p className="mt-3 text-xs text-muted-foreground">Measuring: the first numbers come a minute after the server starts.</p>}
        </Section>

        <Section title="Storage" icon={<Database className="h-4 w-4" aria-hidden="true" />}>
          <dl className="grid grid-cols-2 gap-4">
            <Figure label="Database" value={r.storage.databaseBytes == null ? '–' : bytes(r.storage.databaseBytes)} />
            <Figure
              label="Files"
              value={r.storage.filesReachable ? 'Working' : 'Unreachable'}
              hint={r.storage.filesOutside ? 'In the files bucket' : 'On this server'}
            />
          </dl>
        </Section>

        <Section title="Last backup" icon={<HardDrive className="h-4 w-4" aria-hidden="true" />}>
          <dl>
            <Figure label="Finished" value={backup.text} hint={backup.hint} />
          </dl>
          {r.backup?.problem && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{r.backup.problem}</p>}
        </Section>
      </div>

      <Section title="The last 24 hours" icon={<MemoryStick className="h-4 w-4" aria-hidden="true" />}>
        <div className="grid gap-6 sm:grid-cols-2" data-testid="system-history">
          <Chart points={points} pick={(p) => p.cpu} label="Processors" unit="%" max={100} stepMinutes={step} />
          <Chart points={points} pick={(p) => p.memory} label="Memory" unit="%" max={100} stepMinutes={step} />
          <Chart points={points} pick={(p) => p.inCalls} label="People in calls" unit="" stepMinutes={step} />
          <Chart points={points} pick={(p) => p.requests / Math.max(p.minutes || step, 1)} label="Requests a minute" unit="" stepMinutes={step} />
        </div>
        <p className="mt-3 text-xs text-muted-foreground">Each point is {step} minutes: the busiest minute for processors, memory and calls, and the average for requests. A gap is a time nothing was recorded, such as while the server was off.</p>
      </Section>
    </div>
  );
}
