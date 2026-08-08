import { Link, useNavigate } from 'react-router-dom';
import { useState } from 'react';
import { Hash, AlertCircle, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Separator } from '@/shared/components/ui/separator';
import { api } from '@/shared/api/client';
import { useAuthStore } from '@/shared/store/authStore';

export function LoginPage() {
  const navigate = useNavigate();
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
      setUser(res.data.user);
      navigate('/chat');
    } catch (err: any) {
      setError(err.response?.data?.error || 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const handleOAuthLogin = (provider: string) => {
    window.location.href = `/api/auth/login/${provider}`;
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <Link to="/" className="inline-flex items-center gap-2 justify-center mb-4">
            <Hash className="h-10 w-10 text-primary" />
            <span className="font-bold text-2xl">TeamUp</span>
          </Link>
          <CardTitle>Welcome back</CardTitle>
          <CardDescription>Sign in to your account</CardDescription>
        </CardHeader>
        <CardContent>
          {/* OAuth buttons */}
          <div className="grid grid-cols-2 gap-3 mb-6">
            <Button variant="outline" onClick={() => handleOAuthLogin('google')} disabled={loading}>
              <svg className="mr-2 h-4 w-4" viewBox="0 0 24 24"><path fill="currentColor" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"/><path fill="currentColor" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"/><path fill="currentColor" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"/><path fill="currentColor" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"/></svg>
              Google
            </Button>
            <Button variant="outline" onClick={() => handleOAuthLogin('entra')} disabled={loading}>
              <svg className="mr-2 h-4 w-4" viewBox="0 0 24 24"><path fill="currentColor" d="M20.5 11.335V5.705c0-1.08-.95-1.815-2-1.281-2.018 1.083-2.83 3.373-3.188 4.95l1.825 1.569c.466-.542.99-.956 1.516-1.149C19.77 9.535 20.5 10.285 20.5 11.335zm-16.5 0v-5.63c0-1.08.95-1.815 2-1.281 2.018 1.083 2.83 3.373 3.188 4.95l-1.825 1.569c-.466-.542-.99-.956-1.516-1.149C4.23 9.535 3.5 10.285 3.5 11.335zm-1.825 5.733l1.825-1.569c.358 1.87 1.338 3.566 3.188 4.95 1.05-.534 2-1.269 2-2.35 0-1.05-.95-1.78-2-2.35l-1.825 1.569c-.358 1.87-1.338 3.566-3.188 4.95-1.05-.534-2-1.269-2-2.35v0zm10.588 3.35l2.575 3.462c-1.231.688-2.774 1.1-4.4 1.1-3.54 0-6.395-2.612-6.395-6 0-3.388 2.855-6 6.395-6 1.626 0 3.169.412 4.4 1.1l-2.575 3.462c-.923-1.187-2.412-2.06-4.125-2.06-2.644 0-4.855 1.948-5.188 4.5 2.665-.824 5.422-1.281 8.225-1.281 4.569 0 8.25 3.046 8.25 6.9 0 2.382-1.17 4.502-3.044 5.85z"/></svg>
              Microsoft
            </Button>
          </div>

          <Separator className="my-6" />

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
              <div className="flex items-center justify-between">
                <Label htmlFor="password">Password</Label>
                <Link to="/forgot-password" className="text-sm text-primary hover:underline">
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
        <CardFooter className="flex justify-center">
          <p className="text-sm text-muted-foreground">
            Need an account? Ask an administrator of your organization to add you.
          </p>
        </CardFooter>
      </Card>
    </div>
  );
}
