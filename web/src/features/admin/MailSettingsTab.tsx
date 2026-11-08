import { useEffect, useState, type ChangeEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, Loader2, Send } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { orgApi, type MailSettings } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

/** Why a welcome email did not go, in words for the administrator. */
export function mailProblemText(problem?: string): string {
  if (!problem || problem === 'not_configured') {
    return 'No email was sent, because no mail server is set up. An administrator can set one up under Organization, Email.';
  }
  return `The email could not be sent: ${problem}.`;
}

interface Form {
  host: string;
  port: string;
  user: string;
  password: string;
  from: string;
}

const EMPTY: Form = { host: '', port: '587', user: '', password: '', from: '' };

function formOf(saved?: MailSettings): Form {
  if (!saved || !saved.host) return EMPTY;
  return { host: saved.host, port: String(saved.port || 587), user: saved.user, password: '', from: saved.from };
}

/**
 * The mail server new members' sign-in details go out through. It used to be set
 * only during first-time setup, so an installation set up without one could never
 * send them.
 */
export function MailSettingsTab() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data, isPending, error } = useQuery({ queryKey: ['mail-settings'], queryFn: () => orgApi.getMail() });
  const saved = data?.data;
  const [form, setForm] = useState<Form>(EMPTY);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => setForm(formOf(saved)), [saved]);

  const set = (key: keyof Form) => (e: ChangeEvent<HTMLInputElement>) => {
    setForm((f) => ({ ...f, [key]: e.target.value }));
    setResult(null);
  };
  const dirty = JSON.stringify(form) !== JSON.stringify(formOf(saved));
  const port = Number(form.port);
  const missing = !form.host.trim() ? 'Enter the mail server.' : !form.from.trim() ? 'Enter the address mail comes from.' : null;

  const save = async () => {
    setSaving(true);
    setResult(null);
    try {
      const res = await orgApi.saveMail({ host: form.host, port, user: form.user, password: form.password, from: form.from });
      queryClient.setQueryData(['mail-settings'], res);
      // Saving the same server with a new password returns what was there before, so
      // the query keeps its old object and the effect above never runs. Reset here.
      setForm(formOf(res.data));
      toast.success('Mail settings saved. Send a test email to check them.');
    } catch (err) {
      setResult({ ok: false, text: apiErrorMessage(err, 'Could not save the mail settings.') });
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    setResult(null);
    try {
      const res = await orgApi.testMail();
      setResult(
        res.data.sent
          ? { ok: true, text: `Sent to ${res.data.sentTo}. Check that inbox, and the spam folder.` }
          : { ok: false, text: `The mail server did not take it: ${res.data.problem}` }
      );
    } catch (err) {
      setResult({ ok: false, text: apiErrorMessage(err, 'The test email could not be sent.') });
    } finally {
      setTesting(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Email</CardTitle>
        <p className="text-sm text-muted-foreground">
          New members are emailed their sign-in details through this mail server. Without one, you pass their
          password on yourself.
        </p>
      </CardHeader>
      <CardContent>
        {isPending ? (
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" aria-label="Loading" />
        ) : error ? (
          <p className="text-sm text-destructive">{apiErrorMessage(error, 'Could not load the mail settings.')}</p>
        ) : (
          <form
            className="max-w-xl space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (!missing) void save();
            }}
          >
            <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
              <div className="min-w-0 space-y-2">
                <Label htmlFor="mailHost">Mail server</Label>
                <Input id="mailHost" value={form.host} onChange={set('host')} placeholder="smtp.gmail.com" autoComplete="off" />
              </div>
              <div className="min-w-0 space-y-2">
                <Label htmlFor="mailPort">Port</Label>
                <Input id="mailPort" inputMode="numeric" value={form.port} onChange={set('port')} placeholder="587" />
              </div>
            </div>
            <p className="-mt-2 text-xs text-muted-foreground">
              Use port 587 or 465. Port 25 is blocked on most cloud servers, AWS included.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="min-w-0 space-y-2">
                <Label htmlFor="mailUser">Username</Label>
                <Input id="mailUser" value={form.user} onChange={set('user')} placeholder="you@gmail.com" autoComplete="off" />
              </div>
              <div className="min-w-0 space-y-2">
                <Label htmlFor="mailPassword">Password</Label>
                <Input
                  id="mailPassword"
                  type="password"
                  value={form.password}
                  onChange={set('password')}
                  placeholder={saved?.passwordSet ? 'Saved. Leave blank to keep it.' : 'For Gmail, an app password'}
                  autoComplete="new-password"
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="mailFrom">Send as</Label>
              <Input id="mailFrom" value={form.from} onChange={set('from')} placeholder="TeamUp <you@gmail.com>" autoComplete="off" />
            </div>

            {result && (
              <p role="status" className={result.ok ? 'flex items-start gap-2 text-sm text-emerald-700 dark:text-emerald-400' : 'text-sm text-destructive'}>
                {result.ok && <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />}
                <span className="min-w-0 break-anywhere">{result.text}</span>
              </p>
            )}

            <div className="flex flex-wrap items-center gap-2">
              <Button type="submit" disabled={!!missing || !dirty || saving} title={missing ?? undefined}>
                {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                Save
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={test}
                disabled={!saved?.configured || dirty || testing}
                title={dirty ? 'Save first.' : !saved?.configured ? 'Save the settings first.' : undefined}
              >
                {testing ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Send className="mr-2 h-4 w-4" />}
                Send test email
              </Button>
              {dirty && saved?.configured && <span className="text-xs text-muted-foreground">Save before testing.</span>}
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
