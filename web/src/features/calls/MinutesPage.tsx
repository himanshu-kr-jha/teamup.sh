import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import { AlertTriangle, ChevronLeft, Copy, Loader2, NotebookPen, RefreshCw } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { notetakerApi, type MeetingMinutes } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

const WAITING: Partial<Record<MeetingMinutes['status'], string>> = {
  taking: 'Notes are still being taken: the call has not ended.',
  writing: 'The call has ended and the minutes are being written. This takes a minute or two.',
  failed: 'The minutes could not be written.',
  empty: 'Nothing was heard that could be written down.',
};

/** What was said, in order, with the time and who said it. */
function Transcript({ lines }: { lines: MeetingMinutes['transcript'] }) {
  return (
    <section aria-label="Transcript" className="space-y-2 rounded-lg border border-border p-4 text-left text-sm">
      {lines.map((s, i) => (
        <p key={i}>
          <span className="tabular-nums text-muted-foreground">{format(new Date(s.startedAt), 'HH:mm')}</span>{' '}
          <span className="font-medium">{s.speaker}</span>: {s.text}
        </p>
      ))}
    </section>
  );
}

/** Minutes as text, for pasting into an email or a document. */
function asText(m: NonNullable<MeetingMinutes['minutes']>): string {
  const lines = [`Minutes: ${m.title}`, format(new Date(m.date), 'EEEE d MMMM yyyy, HH:mm'), '', `Attendees: ${m.attendees.join(', ')}`, '', 'Summary', m.summary];
  if (m.decisions.length) lines.push('', 'Decisions', ...m.decisions.map((d) => `- ${d}`));
  if (m.actionItems.length) lines.push('', 'Action items', ...m.actionItems.map((a) => `- ${a.owner}: ${a.task}${a.due ? ` (by ${a.due})` : ''}`));
  if (m.openQuestions.length) lines.push('', 'Open questions', ...m.openQuestions.map((q) => `- ${q}`));
  return lines.join('\n');
}

/** The minutes of one call, for whoever was in it. */
export function MinutesPage() {
  const { callId = '' } = useParams();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [showTranscript, setShowTranscript] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const { data, error, isPending } = useQuery({
    queryKey: ['minutes', callId],
    queryFn: async () => (await notetakerApi.minutes(callId)).data,
    // Keeps looking while the minutes are still being written.
    refetchInterval: (q) => (q.state.data && (q.state.data.status === 'taking' || q.state.data.status === 'writing') ? 10_000 : false),
  });
  const m = data?.minutes;

  const retry = async () => {
    setRetrying(true);
    try {
      await notetakerApi.retry(callId);
      await queryClient.invalidateQueries({ queryKey: ['minutes', callId] });
      void queryClient.invalidateQueries({ queryKey: ['meeting-notes'] });
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not start writing the minutes again.'));
    } finally {
      setRetrying(false);
    }
  };

  return (
    <div className="h-full overflow-y-auto bg-background">
      <div className="mx-auto max-w-3xl px-4 py-6 md:py-8">
        <Button asChild variant="ghost" size="sm" className="-ml-2 mb-4">
          <Link to="/meeting-notes">
            <ChevronLeft className="mr-1 h-4 w-4" />
            Meeting notes
          </Link>
        </Button>

        {isPending ? (
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-label="Loading" />
        ) : error ? (
          <p className="text-sm text-muted-foreground">{apiErrorMessage(error, 'There are no minutes for this call.')}</p>
        ) : !m ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-border p-6 text-center" data-testid="minutes-state">
              {data.status === 'failed' ? (
                <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-destructive" aria-hidden="true" />
              ) : data.status === 'writing' ? (
                <Loader2 className="mx-auto mb-3 h-8 w-8 animate-spin text-muted-foreground" aria-hidden="true" />
              ) : (
                <NotebookPen className="mx-auto mb-3 h-8 w-8 text-muted-foreground" aria-hidden="true" />
              )}
              <p className="font-medium">{WAITING[data.status] ?? 'There are no minutes for this call.'}</p>
              {data.status === 'failed' && data.transcript.length > 0 && (
                <p className="mt-1 text-sm text-muted-foreground">
                  What was said is kept, so they can be written again once the reason below is put right.
                </p>
              )}
              {data.error && (
                <p className="mx-auto mt-3 max-w-xl break-words rounded-md bg-muted px-3 py-2 text-left font-mono text-xs text-muted-foreground">
                  {data.error}
                </p>
              )}
              {(data.status === 'failed' || data.transcript.length > 0) && (
                <div className="mt-4 flex flex-wrap justify-center gap-2">
                  {data.status === 'failed' && data.transcript.length > 0 && (
                    <Button size="sm" onClick={retry} disabled={retrying}>
                      {retrying ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <RefreshCw className="mr-2 h-4 w-4" />}
                      Write the minutes again
                    </Button>
                  )}
                  {data.transcript.length > 0 && (
                    <Button variant="ghost" size="sm" onClick={() => setShowTranscript((v) => !v)} aria-expanded={showTranscript}>
                      {showTranscript ? 'Hide the transcript' : 'Show the transcript'}
                    </Button>
                  )}
                </div>
              )}
            </div>
            {showTranscript && <Transcript lines={data.transcript} />}
          </div>
        ) : (
          <article className="space-y-6" data-testid="minutes">
            <header className="space-y-1">
              <p className="text-sm font-medium text-primary">Minutes</p>
              <h1 className="text-2xl font-bold">{m.title}</h1>
              <p className="text-sm text-muted-foreground">
                {format(new Date(m.date), 'EEEE d MMMM yyyy, HH:mm')} · {m.attendees.join(', ')}
              </p>
            </header>

            <section>
              <h2 className="mb-2 font-semibold">Summary</h2>
              <p className="whitespace-pre-wrap leading-relaxed">{m.summary}</p>
            </section>

            {m.decisions.length > 0 && (
              <section>
                <h2 className="mb-2 font-semibold">Decisions</h2>
                <ul className="list-disc space-y-1 pl-5">
                  {m.decisions.map((d, i) => (
                    <li key={i}>{d}</li>
                  ))}
                </ul>
              </section>
            )}

            {m.actionItems.length > 0 && (
              <section>
                <h2 className="mb-2 font-semibold">Action items</h2>
                <ul className="divide-y divide-border rounded-lg border border-border">
                  {m.actionItems.map((a, i) => (
                    <li key={i} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 p-3">
                      <span className="font-medium">{a.owner}</span>
                      <span className="min-w-0 flex-1">{a.task}</span>
                      {a.due && <span className="text-sm text-muted-foreground">by {a.due}</span>}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {m.openQuestions.length > 0 && (
              <section>
                <h2 className="mb-2 font-semibold">Open questions</h2>
                <ul className="list-disc space-y-1 pl-5">
                  {m.openQuestions.map((q, i) => (
                    <li key={i}>{q}</li>
                  ))}
                </ul>
              </section>
            )}

            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(asText(m));
                    toast.success('Copied the minutes.');
                  } catch {
                    toast.error('Could not copy. Select the text instead.');
                  }
                }}
              >
                <Copy className="mr-2 h-4 w-4" />
                Copy
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setShowTranscript((v) => !v)} aria-expanded={showTranscript}>
                {showTranscript ? 'Hide the transcript' : 'Show the transcript'}
              </Button>
            </div>

            {showTranscript && <Transcript lines={data.transcript} />}
          </article>
        )}
      </div>
    </div>
  );
}
