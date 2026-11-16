import { FormEvent, useEffect, useState } from 'react';
import type { AxiosError } from 'axios';
import { Link, useNavigate } from 'react-router-dom';
import { AlertCircle, KeyRound, Link2Off, Loader2 } from 'lucide-react';
import { BrandMark } from '@/features/branding/BrandMark';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { authApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { signOutLocally } from './session';

const MIN_LENGTH = 12;

/**
 * The token arrives after # in the emailed link, which browsers never send to
 * a server. Once read it is taken out of the address bar, so it is not left in
 * the history or on screen.
 */
function readToken(): string {
  return new URLSearchParams(window.location.hash.slice(1)).get('token') ?? '';
}

const DEAD_LINK = 'It has expired, was already used, or a newer one was sent. Each link works once, for 30 minutes.';

/** The server says a link is dead with a 400; anything else is not about the link. */
function isDeadLink(err: unknown): boolean {
  return (err as AxiosError)?.response?.status === 400 && /expired|already been used/.test(apiErrorMessage(err, ''));
}

function linkProblem(err: unknown): string {
  return isDeadLink(err) ? DEAD_LINK : apiErrorMessage(err, 'Could not check your link. Try opening it again.');
}

type LinkState = { kind: 'checking' } | { kind: 'valid'; email: string } | { kind: 'invalid'; message: string };

/** Choosing a new password with an emailed link. */
export function ResetPasswordPage() {
  const navigate = useNavigate();
  const [token, setToken] = useState(readToken);
  const [link, setLink] = useState<LinkState>(() =>
    token ? { kind: 'checking' } : { kind: 'invalid', message: 'This page needs the link from your reset email.' },
  );
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  // Take the token out of the address bar, and take a newer link pasted into
  // this same tab, which changes only what is after the #.
  useEffect(() => {
    const take = () => {
      const fresh = readToken();
      if (!window.location.hash) return;
      window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search);
      if (fresh) setToken(fresh);
    };
    take();
    window.addEventListener('hashchange', take);
    return () => window.removeEventListener('hashchange', take);
  }, []);

  useEffect(() => {
    if (!token) return;
    setLink({ kind: 'checking' });
    setError('');
    let live = true;
    authApi
      .checkPasswordReset(token)
      .then((res) => live && setLink({ kind: 'valid', email: res.data.email }))
      .catch((err) => live && setLink({ kind: 'invalid', message: linkProblem(err) }));
    return () => {
      live = false;
    };
  }, [token]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (saving || link.kind !== 'valid') return;
    if (newPassword.length < MIN_LENGTH) {
      setError(`Choose a password of at least ${MIN_LENGTH} characters.`);
      return;
    }
    if (newPassword !== confirmPassword) {
      setError('The two passwords do not match.');
      return;
    }
    setSaving(true);
    setError('');
    try {
      await authApi.resetPassword({ token, newPassword });
      // Every session ended, so nothing kept in this browser is any use now.
      await signOutLocally();
      navigate('/login', {
        replace: true,
        state: { email: link.email, notice: 'Your password has been changed. Sign in with the new one.' },
      });
    } catch (err) {
      if (isDeadLink(err)) setLink({ kind: 'invalid', message: linkProblem(err) });
      else setError(apiErrorMessage(err, 'Could not change your password.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <Link to="/" className="mb-4 inline-flex justify-center">
            <BrandMark size="lg" />
          </Link>
          {link.kind === 'invalid' ? (
            <>
              <div className="mx-auto mb-2 flex h-10 w-10 items-center justify-center rounded-full bg-destructive/10">
                <Link2Off className="h-5 w-5 text-destructive" aria-hidden="true" />
              </div>
              <CardTitle>This link does not work</CardTitle>
              <CardDescription>{link.message}</CardDescription>
            </>
          ) : (
            <>
              <div className="mx-auto mb-2 flex h-10 w-10 items-center justify-center rounded-full bg-primary/10">
                <KeyRound className="h-5 w-5 text-primary" aria-hidden="true" />
              </div>
              <CardTitle>Choose a new password</CardTitle>
              <CardDescription>
                {link.kind === 'valid' ? (
                  <>
                    For <span className="font-medium text-foreground">{link.email}</span>. You will be signed out
                    everywhere, then sign in with it.
                  </>
                ) : (
                  'Checking your link...'
                )}
              </CardDescription>
            </>
          )}
        </CardHeader>
        <CardContent>
          {link.kind === 'invalid' ? (
            <Button asChild className="w-full">
              <Link to="/forgot-password">Ask for a new link</Link>
            </Button>
          ) : link.kind === 'checking' ? (
            <div className="flex justify-center py-4" role="status" aria-label="Checking your link">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-4">
              {/* For password managers: which account the new password belongs to. */}
              <input type="email" name="username" autoComplete="username" value={link.email} readOnly hidden />
              <div className="space-y-2">
                <Label htmlFor="newPassword">New password</Label>
                <Input
                  id="newPassword"
                  type="password"
                  autoComplete="new-password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  required
                  autoFocus
                  disabled={saving}
                  aria-describedby="newPasswordHint"
                />
                <p id="newPasswordHint" className="text-xs text-muted-foreground">
                  At least {MIN_LENGTH} characters.
                </p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="confirmPassword">Confirm new password</Label>
                <Input
                  id="confirmPassword"
                  type="password"
                  autoComplete="new-password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  required
                  disabled={saving}
                />
              </div>
              {error && (
                <div className="flex items-center gap-2 text-sm text-destructive" role="alert">
                  <AlertCircle className="h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}
              <Button type="submit" className="w-full" disabled={saving}>
                {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                Change password
              </Button>
            </form>
          )}
        </CardContent>
        <CardFooter className="justify-center">
          <Link to="/login" className="text-sm text-muted-foreground underline underline-offset-2 hover:text-foreground">
            Back to sign in
          </Link>
        </CardFooter>
      </Card>
    </div>
  );
}
