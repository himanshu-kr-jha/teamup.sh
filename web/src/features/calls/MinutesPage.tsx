import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { format } from 'date-fns';
import { ChevronLeft, Copy, Loader2, NotebookPen } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { notetakerApi, type MeetingMinutes } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

const WAITING: Partial<Record<MeetingMinutes['status'], string>> = {
  taking: 'Notes are still being taken: the call has not ended.',
  writing: 'The call has ended and the minutes are being written. This takes a minute or two.',
};

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
  const [showTranscript, setShowTranscript] = useState(false);
  const { data, error, isPending } = useQuery({
    queryKey: ['minutes', callId],
    queryFn: async () => (await notetakerApi.minutes(callId)).data,
    // Keeps looking while the minutes are still being written.
    refetchInterval: (q) => (q.state.data && (q.state.data.status === 'taking' || q.state.data.status === 'writing') ? 10_000 : false),
  });
  const m = data?.minutes;

  return (
    <div className="h-full overflow-y-auto bg-background">
      <div className="mx-auto max-w-3xl px-4 py-6 md:py-8">
        <Button asChild variant="ghost" size="sm" className="-ml-2 mb-4">
          <Link to="/calls">
            <ChevronLeft className="mr-1 h-4 w-4" />
            Calls
          </Link>
        </Button>

        {isPending ? (
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-label="Loading" />
        ) : error ? (
          <p className="text-sm text-muted-foreground">{apiErrorMessage(error, 'There are no minutes for this call.')}</p>
        ) : !m ? (
          <div className="rounded-lg border border-border p-6 text-center">
            <NotebookPen className="mx-auto mb-3 h-8 w-8 text-muted-foreground" aria-hidden="true" />
            <p className="font-medium">{WAITING[data.status] ?? 'There are no minutes for this call.'}</p>
            {data.error && <p className="mt-1 text-sm text-muted-foreground">{data.error}</p>}
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

            {showTranscript && (
              <section aria-label="Transcript" className="space-y-2 rounded-lg border border-border p-4 text-sm">
                {data.transcript.map((s, i) => (
                  <p key={i}>
                    <span className="tabular-nums text-muted-foreground">{format(new Date(s.startedAt), 'HH:mm')}</span>{' '}
                    <span className="font-medium">{s.speaker}</span>: {s.text}
                  </p>
                ))}
              </section>
            )}
          </article>
        )}
      </div>
    </div>
  );
}
