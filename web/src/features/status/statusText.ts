import { addHours, format } from 'date-fns';
import type { StatusReport } from '@/shared/api/client';

type Overall = StatusReport['overall'] | 'unreachable' | undefined;

export function overallText(overall: Overall): string {
  switch (overall) {
    case 'operational':
      return 'Everything is working';
    case 'degraded':
      return 'Some things are not working';
    case 'outage':
      return 'TeamUp is down';
    default:
      return 'TeamUp cannot be reached';
  }
}

/** "1 min", "12 min", "1 h 5 min". */
export function durationText(minutes: number): string {
  if (minutes < 60) return `${minutes} min`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m ? `${h} h ${m} min` : `${h} h`;
}

/** What one bar says when you point at it. */
export function hourText(hour: StatusReport['components'][number]['hours'][number]): string {
  const start = new Date(hour.start);
  const span = `${format(start, 'HH:mm')} to ${format(addHours(start, 1), 'HH:mm')}`;
  switch (hour.status) {
    case 'up':
      return `${span}: working`;
    case 'partial':
      return `${span}: down for ${durationText(hour.downMinutes)}`;
    case 'down':
      return `${span}: down`;
    default:
      return `${span}: no record`;
  }
}

/** The names of what is down, for the banner inside the app. */
export function downNames(report: StatusReport): string[] {
  return report.components.filter((c) => c.status !== 'operational').map((c) => c.name);
}
