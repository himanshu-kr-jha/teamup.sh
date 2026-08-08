import { useState, FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertCircle, KeyRound, Loader2 } from 'lucide-react';
import { authApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useAuthStore } from '@/shared/store/authStore';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/shared/components/ui/card';

/**
 * Where somebody lands after signing in with the password an administrator
 * generated for them. The API refuses everything else until this is done, so
 * this page deliberately has no navigation away from it.
 */
export function ChangePasswordPage() {
  const navigate = useNavigate();
  const logoutStore = useAuthStore((s) => s.logout);
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (saving) return;
    if (newPassword !== confirmPassword) {
      setError('The two new passwords do not match.');
      return;
    }
    if (newPassword.length < 8) {
      setError('Choose a password of at least 8 characters.');
      return;
    }
    setSaving(true);
    setError('');
    try {
      await authApi.changePassword({ currentPassword, newPassword });
      // Changing it revokes every session, this one included, so there is
      // nothing to keep — sign in again with the new password.
      logoutStore();
      navigate('/login', { replace: true });
    } catch (err) {
      setError(apiErrorMessage(err, 'Could not change your password.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <div className="mx-auto mb-2 flex h-10 w-10 items-center justify-center rounded-full bg-primary/10">
            <KeyRound className="h-5 w-5 text-primary" aria-hidden="true" />
          </div>
          <CardTitle>Choose your password</CardTitle>
          <CardDescription>
            You signed in with a password someone else generated. Set your own before
            you carry on.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="currentPassword">Temporary password</Label>
              <Input
                id="currentPassword"
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                required
                disabled={saving}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="newPassword">New password</Label>
              <Input
                id="newPassword"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                required
                disabled={saving}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirmPassword">Confirm new password</Label>
              <Input
                id="confirmPassword"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                required
                disabled={saving}
              />
            </div>
            {error && (
              <div className="flex items-center gap-2 text-sm text-destructive" role="alert">
                <AlertCircle className="h-4 w-4" />
                <span>{error}</span>
              </div>
            )}
            <Button type="submit" className="w-full" disabled={saving}>
              {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
              Set password and sign in again
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
