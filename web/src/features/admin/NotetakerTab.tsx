import { useEffect, useState, type ChangeEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, Loader2, NotebookPen } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Switch } from '@/shared/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { notetakerApi, type NotetakerSettings } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

interface Form {
  enabled: boolean;
  sttKey: string;
  sttModel: string;
  sttLanguage: string;
  sttBaseUrl: string;
  llmProvider: NotetakerSettings['llmProvider'];
  llmKey: string;
  llmModel: string;
  llmBaseUrl: string;
}

function formOf(s?: NotetakerSettings): Form {
  return {
    enabled: s?.enabled ?? false,
    sttKey: '',
    sttModel: s?.sttModel ?? '',
    sttLanguage: s?.sttLanguage || 'en-IN',
    sttBaseUrl: s?.sttBaseUrl ?? '',
    llmProvider: s?.llmProvider ?? 'anthropic',
    llmKey: '',
    llmModel: s?.llmModel ?? '',
    llmBaseUrl: s?.llmBaseUrl ?? '',
  };
}

const LANGUAGES = [
  { value: 'en-IN', label: 'English' },
  { value: 'hi-IN', label: 'Hindi' },
  { value: 'unknown', label: 'Detect for each speaker' },
];

const MODEL_HINT: Record<Form['llmProvider'], string> = {
  anthropic: 'Leave empty for claude-sonnet-5-5.',
  openai: 'The model to use, for example gpt-4.1.',
  nvidia: 'Leave empty for nvidia/nemotron-3-super-120b-a12b, or name another model from build.nvidia.com. If NVIDIA retires the one named, another Nemotron model writes the minutes.',
  'openai-compatible': 'The model name your server expects, for example nvidia/nemotron-3-super-120b-a12b.',
};

/**
 * The meeting note-taker, for administrators only: on or off for every call and
 * meeting, and the services it uses. Keys are never shown again once saved.
 */
export function NotetakerTab() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data, isPending, error } = useQuery({ queryKey: ['notetaker-settings'], queryFn: () => notetakerApi.get() });
  const saved = data?.data;
  const [form, setForm] = useState<Form>(formOf());
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState('');

  useEffect(() => setForm(formOf(saved)), [saved]);

  const set = (key: keyof Form) => (e: ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [key]: e.target.value }));

  const save = async (next: Form = form) => {
    setSaving(true);
    setProblem('');
    try {
      const res = await notetakerApi.save({
        enabled: next.enabled,
        sttKey: next.sttKey || undefined,
        sttModel: next.sttModel,
        sttLanguage: next.sttLanguage,
        sttBaseUrl: next.sttBaseUrl,
        llmProvider: next.llmProvider,
        llmKey: next.llmKey || undefined,
        llmModel: next.llmModel,
        llmBaseUrl: next.llmBaseUrl,
      });
      queryClient.setQueryData(['notetaker-settings'], res);
      setForm(formOf(res.data));
      toast.success(res.data.ready ? 'Saved. Notes are taken in every call and meeting.' : 'Saved.');
    } catch (err) {
      setProblem(apiErrorMessage(err, 'Could not save the note-taker settings.'));
    } finally {
      setSaving(false);
    }
  };

  if (isPending) return <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" aria-label="Loading" />;
  if (error) return <p className="text-sm text-destructive">{apiErrorMessage(error, 'Could not load the note-taker settings.')}</p>;

  const own = form.llmProvider === 'openai-compatible';

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <NotebookPen className="h-5 w-5" aria-hidden="true" />
          Meeting notes
        </CardTitle>
        <p className="text-sm text-muted-foreground">
          A participant called Notes joins every call and meeting, visibly, and writes down what is said. When it
          ends, everybody who was there gets the minutes: a summary, decisions, action items and open questions.
          Only administrators can turn it off.
        </p>
      </CardHeader>
      <CardContent>
        <form
          className="max-w-xl space-y-6"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <div className="flex items-start justify-between gap-4 rounded-lg border border-border p-4">
            <div>
              <Label htmlFor="notesOn" className="text-base">
                Take notes in every call and meeting
              </Label>
              <p className="mt-1 text-sm text-muted-foreground" data-testid="notetaker-state">
                {saved?.ready ? (
                  <span className="flex items-center gap-1 text-emerald-700 dark:text-emerald-400">
                    <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
                    On, and ready.
                  </span>
                ) : (
                  saved?.notReady || 'Not set up yet.'
                )}
              </p>
            </div>
            <Switch
              id="notesOn"
              checked={form.enabled}
              disabled={saving}
              onCheckedChange={(enabled) => {
                const next = { ...form, enabled };
                setForm(next);
                void save(next);
              }}
            />
          </div>

          <fieldset className="space-y-4">
            <legend className="font-medium">Speech to text: Sarvam</legend>
            <div className="space-y-2">
              <Label htmlFor="sttKey">Sarvam API key</Label>
              <Input
                id="sttKey"
                type="password"
                autoComplete="off"
                value={form.sttKey}
                onChange={set('sttKey')}
                placeholder={saved?.sttKeySet ? 'Saved. Type a new one to replace it.' : 'From dashboard.sarvam.ai'}
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="sttLanguage">Language spoken</Label>
                <Select value={form.sttLanguage} onValueChange={(v) => setForm((f) => ({ ...f, sttLanguage: v }))}>
                  <SelectTrigger id="sttLanguage"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {LANGUAGES.map((l) => (
                      <SelectItem key={l.value} value={l.value}>{l.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="sttModel">Model</Label>
                <Input id="sttModel" value={form.sttModel} onChange={set('sttModel')} placeholder="Sarvam's default" />
              </div>
            </div>
          </fieldset>

          <fieldset className="space-y-4">
            <legend className="font-medium">Writing the minutes</legend>
            <div className="space-y-2">
              <Label htmlFor="llmProvider">Model</Label>
              <Select
                value={form.llmProvider}
                onValueChange={(v) => setForm((f) => ({ ...f, llmProvider: v as Form['llmProvider'] }))}
              >
                <SelectTrigger id="llmProvider"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="anthropic">Claude (Anthropic)</SelectItem>
                  <SelectItem value="openai">OpenAI</SelectItem>
                  <SelectItem value="nvidia">NVIDIA (Nemotron and others, build.nvidia.com)</SelectItem>
                  <SelectItem value="openai-compatible">Your own model, such as Nemotron</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {own && (
              <div className="space-y-2">
                <Label htmlFor="llmBaseUrl">Address of your model</Label>
                <Input
                  id="llmBaseUrl"
                  value={form.llmBaseUrl}
                  onChange={set('llmBaseUrl')}
                  placeholder="http://gpu-server:8000/v1"
                  autoComplete="off"
                />
                <p className="text-xs text-muted-foreground">
                  Anything that serves the OpenAI chat API: vLLM, NVIDIA NIM, Ollama and others.
                </p>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="llmModel">Model name</Label>
              <Input id="llmModel" value={form.llmModel} onChange={set('llmModel')} autoComplete="off" />
              <p className="text-xs text-muted-foreground">{MODEL_HINT[form.llmProvider]}</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="llmKey">API key{own ? ', if your server needs one' : ''}</Label>
              <Input
                id="llmKey"
                type="password"
                autoComplete="off"
                value={form.llmKey}
                onChange={set('llmKey')}
                placeholder={
                  saved?.llmKeySet
                    ? 'Saved. Type a new one to replace it.'
                    : form.llmProvider === 'nvidia'
                      ? 'nvapi-…'
                      : ''
                }
              />
            </div>
          </fieldset>

          {problem && (
            <p className="text-sm text-destructive" role="alert">
              {problem}
            </p>
          )}
          <Button type="submit" disabled={saving}>
            {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            Save
          </Button>
          <p className="text-xs text-muted-foreground">
            What people say is sent to Sarvam to be written down, and the transcript to the model you choose to write
            the minutes. Transcripts and minutes are kept with the call, readable by the people who were in it and by
            administrators.
          </p>
        </form>
      </CardContent>
    </Card>
  );
}
