import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Loader2 } from 'lucide-react';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage, isClientError } from '@/shared/api/errors';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { describeEvent, formatMinutes, orderEvents, peopleFromSessions } from './callLog';

/**
 * Who asked to join, who let them in, who came, when they left, who shared. The
 * server decides who may read it; anybody else gets a plain answer rather than a
 * button quietly missing.
 */
export function CallLogDialog({
  callId,
  open,
  onOpenChange,
  live,
}: {
  callId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Still going, so keep it up to date while it is open. */
  live?: boolean;
}) {
  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['call-log', callId],
    queryFn: () => callsApi.log(callId).then((res) => res.data),
    enabled: open,
    staleTime: 5_000,
    retry: false,
    refetchInterval: open && live ? 10_000 : false,
  });

  const people = data ? peopleFromSessions(data.sessions ?? []) : [];
  const events = orderEvents(data?.events ?? []);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Call log</DialogTitle>
          <DialogDescription>Everything that happened in this call. It cannot be edited.</DialogDescription>
        </DialogHeader>

        {isPending ? (
          <div role="status" className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
            Loading the log
          </div>
        ) : !data ? (
          <div role="alert" className="py-8 text-center text-sm">
            <AlertTriangle className="mx-auto mb-2 h-5 w-5 text-muted-foreground" aria-hidden="true" />
            {isClientError(error) ? (
              <p>You do not have access to the log of this call.</p>
            ) : (
              <>
                <p>{apiErrorMessage(error, 'Could not load the log.')}</p>
                <Button variant="outline" size="sm" className="mt-3" onClick={() => refetch()}>
                  Try again
                </Button>
              </>
            )}
          </div>
        ) : (
          <div className="min-w-0 space-y-4">
            {!data.attendanceRecorded && (
              <p className="rounded-md border border-border bg-muted/50 p-3 text-sm" role="note">
                <span className="font-medium">No attendance data for this call.</span>{' '}
                The media server never reported a connection. Either nobody connected, or its reports did
                not reach TeamUp, so this does not show that nobody attended.
              </p>
            )}

            {data.attendanceRecorded && (
              <section>
                <h3 className="mb-1 text-sm font-medium">Who was there</h3>
                <ul className="divide-y divide-border rounded-md border border-border">
                  {people.map((person) => (
                    <li key={person.userId} className="flex flex-wrap items-baseline justify-between gap-x-3 px-3 py-2 text-sm">
                      <span className="min-w-0 break-anywhere font-medium">{person.name}</span>
                      <span className="text-xs tabular-nums text-muted-foreground">
                        {person.firstJoinAt ? formatTime(person.firstJoinAt) : 'Unknown'} to{' '}
                        {person.stillHere ? 'now' : person.lastLeaveAt ? formatTime(person.lastLeaveAt) : 'unknown'}
                        {', '}
                        {formatMinutes(person.minutes)}
                        {person.sessions > 1 && `, ${person.sessions} connections`}
                      </span>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section>
              <h3 className="mb-1 text-sm font-medium">What happened</h3>
              {events.length === 0 ? (
                <p className="text-sm text-muted-foreground">Nothing recorded yet.</p>
              ) : (
                <ol className="max-h-72 divide-y divide-border overflow-y-auto rounded-md border border-border">
                  {events.map((event) => (
                    <li key={event.id} className="flex items-baseline justify-between gap-3 px-3 py-1.5 text-sm">
                      <span className="min-w-0 break-anywhere">{describeEvent(event)}</span>
                      <time
                        dateTime={event.at}
                        title={formatDate(event.at)}
                        className="shrink-0 text-xs tabular-nums text-muted-foreground"
                      >
                        {formatTime(event.at)}
                      </time>
                    </li>
                  ))}
                </ol>
              )}
            </section>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
