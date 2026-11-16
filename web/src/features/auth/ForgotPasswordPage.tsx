import { FormEvent, useState } from 'react';
import type { AxiosError } from 'axios';
import { Link, useLocation } from 'react-router-dom';
import { AlertCircle, Loader2, MailCheck } from 'lucide-react';
import { BrandMark } from '@/features/branding/BrandMark';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { authApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';

/**
 * Asking for a reset link. What it says afterwards is the same whether or not
 * there is an account at the address, because the server's answer is.
 */
export function ForgotPasswordPage() {
  const location = useLocation();
  const [email, setEmail] = useState(() => (location.state as { email?: string } | null)?.email ?? '');
  const [sentTo, setSentTo] = useState('');
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (sending) return;
    setError('');
    setSending(true);
    try {
      await authApi.forgotPassword(email.trim());
      setSentTo(email.trim());
    } catch (err) {
      setError(
        (err as AxiosError)?.response?.status === 429
          ? 'Too many reset links have been asked for from here. Try again later, or ask an administrator of your organization to reset your password.'
          : apiErrorMessage(err, 'Could not ask for a reset link. Try again in a moment.'),
      );
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <Link to="/" className="mb-4 inline-flex justify-center">
            <BrandMark size="lg" />
          </Link>
          {sentTo ? (
            <>
              <div className="mx-auto mb-2 flex h-10 w-10 items-center justify-center rounded-full bg-primary/10">
                <MailCheck className="h-5 w-5 text-primary" aria-hidden="true" />
              </div>
              <CardTitle>Check your email</CardTitle>
              <CardDescription>
                If there is an account at <span className="font-medium text-foreground">{sentTo}</span>, a link to
                choose a new password is on its way. It works once, for 30 minutes.
              </CardDescription>
            </>
          ) : (
            <>
              <CardTitle>Forgot your password?</CardTitle>
              <CardDescription>Enter the email address you sign in with, and we will send you a link to choose a new one.</CardDescription>
            </>
          )}
        </CardHeader>
        <CardContent>
          {sentTo ? (
            <div className="space-y-4">
              <p className="text-sm text-muted-foreground">
                Nothing after a few minutes? Look in your spam folder, check the address, or ask an administrator of your
                organization to reset your password. If your organization has no email set up, its administrators have
                already been told you asked.
              </p>
              <Button type="button" variant="outline" className="w-full" onClick={() => setSentTo('')}>
                Use a different address
              </Button>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  type="email"
                  autoComplete="email"
                  placeholder="you@company.com"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  autoFocus
                  disabled={sending}
                />
              </div>
              {error && (
                <div className="flex items-center gap-2 text-sm text-destructive" role="alert">
                  <AlertCircle className="h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}
              <Button type="submit" className="w-full" disabled={sending}>
                {sending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                Send reset link
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
