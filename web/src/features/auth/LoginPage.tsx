import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useState } from 'react';
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-react';
import { BrandMark } from '@/features/branding/BrandMark';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { api } from '@/shared/api/client';
import { useAuthStore } from '@/shared/store/authStore';
import { safeNext } from './nextPath';

export function LoginPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const setUser = useAuthStore((s) => s.setUser);
  // Back from choosing a new password: say so, and keep the address.
  const arrived = useLocation().state as { email?: string; notice?: string } | null;
  const [email, setEmail] = useState(arrived?.email ?? '');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const res = await api.post('/auth/login', { email, password });
      setUser({ ...res.data.user, permissions: res.data.permissions ?? [] });
      navigate(safeNext(params.get('next')));
    } catch (err: any) {
      setError(err.response?.data?.error || 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <Link to="/" className="mb-4 inline-flex justify-center">
            <BrandMark size="lg" />
          </Link>
          <CardTitle>Welcome back</CardTitle>
          <CardDescription>Sign in to your account</CardDescription>
        </CardHeader>
        <CardContent>
          {/* Email/Password form */}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                placeholder="you@company.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                disabled={loading}
              />
            </div>
            <div className="space-y-2">
              <div className="flex items-baseline justify-between gap-2">
                <Label htmlFor="password">Password</Label>
                <Link
                  to="/forgot-password"
                  state={{ email }}
                  className="text-sm text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                >
                  Forgot password?
                </Link>
              </div>
              <Input
                id="password"
                type="password"
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                disabled={loading}
              />
            </div>
            {arrived?.notice && !error && (
              <div className="flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-400" role="status">
                <CheckCircle2 className="h-4 w-4 shrink-0" />
                <span>{arrived.notice}</span>
              </div>
            )}
            {error && (
              <div className="flex items-center gap-2 text-sm text-destructive" role="alert">
                <AlertCircle className="h-4 w-4" />
                <span>{error}</span>
              </div>
            )}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
              Sign in
            </Button>
          </form>
        </CardContent>
        <CardFooter className="flex flex-col items-center gap-2">
          <p className="text-center text-sm text-muted-foreground">
            Need an account? Ask an administrator of your organization to add you.
          </p>
          <Link to="/status" className="text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground">
            Service status
          </Link>
        </CardFooter>
      </Card>
    </div>
  );
}
