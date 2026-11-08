import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useState } from 'react';
import { AlertCircle, Loader2 } from 'lucide-react';
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
  const [email, setEmail] = useState('');
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
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                disabled={loading}
              />
              <p className="text-sm text-muted-foreground">
                Forgot your password? Ask an administrator of your organization to reset it.
              </p>
            </div>
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
