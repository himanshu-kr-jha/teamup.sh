import { useState, useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { User, Bell, Shield, Palette, Loader2, Save, Monitor } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/components/ui/tabs';
import { PushSettings } from '@/features/notifications/PushSettings';
import { Separator } from '@/shared/components/ui/separator';
import { useAuth } from '@/features/auth/AuthProvider';
import { authApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { FormDialog } from '@/shared/components/FormDialog';
import { formatRelativeTime } from '@/shared/utils/cn';

export function SettingsPage() {
  const { user, refreshUser } = useAuth();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState('profile');
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState(user?.name || '');
  const [theme, setTheme] = useState(() => localStorage.getItem('theme') || 'system');
  const [passwordOpen, setPasswordOpen] = useState(false);

  // The user may still be loading on a hard refresh; without this the name
  // field stays empty and Save is disabled even once it arrives.
  useEffect(() => {
    if (user?.name) setName((current) => current || user.name);
  }, [user?.name]);

  // The save used to PATCH a path that carried the API prefix twice, against a
  // route that does not exist, and swallow the 404 into the console — so it
  // always looked like it worked. Address the signed-in user by id.
  const handleSave = async () => {
    if (!user?.id) return;
    setSaving(true);
    try {
      await orgApi.updateUser(user.id, { name });
      await refreshUser();
      toast.success('Your profile has been saved.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not save your profile.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col bg-background">
      <div className="flex h-16 items-center justify-between px-4 md:px-6 border-b border-border">
        <h1 className="text-2xl font-bold">Settings</h1>
        <Button onClick={handleSave} disabled={saving || !name.trim()}>
          {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
          Save Changes
        </Button>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 overflow-auto">
        <TabsList className="border-b border-border p-4 md:p-6">
          <TabsTrigger value="profile"><User className="mr-2 h-4 w-4" />Profile</TabsTrigger>
          <TabsTrigger value="notifications"><Bell className="mr-2 h-4 w-4" />Notifications</TabsTrigger>
          <TabsTrigger value="security"><Shield className="mr-2 h-4 w-4" />Security</TabsTrigger>
          <TabsTrigger value="appearance"><Palette className="mr-2 h-4 w-4" />Appearance</TabsTrigger>
        </TabsList>

        <TabsContent value="profile" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader><CardTitle>Profile</CardTitle></CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="profileName">Display name</Label>
                <Input id="profileName" value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="space-y-2">
                <Label htmlFor="profileEmail">Email</Label>
                <Input id="profileEmail" value={user?.email || ''} disabled />
                <p className="text-xs text-muted-foreground">
                  Your email comes from your sign-in account and cannot be changed here.
                </p>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="notifications" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <PushSettings />
        </TabsContent>

        <TabsContent value="security" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader><CardTitle>Security</CardTitle></CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label>Password</Label>
                <p className="text-sm text-muted-foreground">
                  Changing your password signs you out everywhere, including this device.
                </p>
                <Button variant="outline" onClick={() => setPasswordOpen(true)}>Change password</Button>
              </div>
              <Separator />
              <SessionsSection />
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="appearance" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader><CardTitle>Appearance</CardTitle></CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="theme">Theme</Label>
                <Select
                  value={theme}
                  onValueChange={(v) => {
                    setTheme(v);
                    localStorage.setItem('theme', v);
                    document.documentElement.classList.toggle(
                      'dark',
                      v === 'dark' || (v === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
                    );
                  }}
                >
                  <SelectTrigger id="theme"><SelectValue placeholder="Select theme" /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="system">System</SelectItem>
                    <SelectItem value="light">Light</SelectItem>
                    <SelectItem value="dark">Dark</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <ChangePasswordDialog
        open={passwordOpen}
        onOpenChange={setPasswordOpen}
        onChanged={() => {
          queryClient.clear();
          toast.success('Password changed. Please sign in again.');
          window.location.assign('/login');
        }}
      />
    </div>
  );
}

function SessionsSection() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => authApi.listSessions(),
  });
  const sessions = data?.data ?? [];

  const revoke = async (sessionId: string) => {
    try {
      await authApi.revokeSession(sessionId);
      await queryClient.invalidateQueries({ queryKey: ['sessions'] });
      toast.success('That device has been signed out.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not sign that device out.'));
    }
  };

  return (
    <div className="space-y-3">
      <Label>Active sessions</Label>
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {!isLoading && sessions.length === 0 && (
        <p className="text-sm text-muted-foreground">No other active sessions.</p>
      )}
      <ul className="space-y-2">
        {sessions.map((s: any) => (
          <li key={s.sessionId} className="flex items-center gap-3 rounded-lg border border-border p-3">
            <Monitor className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{s.userAgent || 'Unknown device'}</p>
              <p className="text-xs text-muted-foreground">
                {s.ipAddress || 'unknown address'} · started {formatRelativeTime(s.createdAt)}
                {s.current && ' · this device'}
              </p>
            </div>
            {!s.current && (
              <Button variant="outline" size="sm" onClick={() => revoke(s.sessionId)}>
                Sign out
              </Button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function ChangePasswordDialog({
  open,
  onOpenChange,
  onChanged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onChanged: () => void;
}) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');

  const mismatch = next !== '' && confirm !== '' && next !== confirm;
  const tooShort = next !== '' && next.length < 12;

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          setCurrent('');
          setNext('');
          setConfirm('');
        }
        onOpenChange(o);
      }}
      title="Change password"
      description="You will be signed out on every device, including this one."
      submitLabel="Change password"
      disabledReason={
        !current || !next || !confirm
          ? 'Fill in every field'
          : tooShort
            ? 'Use at least 12 characters'
            : mismatch
              ? 'The new passwords do not match'
              : undefined
      }
      onSubmit={async () => {
        await authApi.changePassword({ currentPassword: current, newPassword: next });
        onChanged();
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="currentPassword">Current password</Label>
        <Input id="currentPassword" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} />
      </div>
      <div className="space-y-2">
        <Label htmlFor="newPassword">New password</Label>
        <Input id="newPassword" type="password" value={next} onChange={(e) => setNext(e.target.value)} />
        {tooShort && <p className="text-xs text-destructive">Use at least 12 characters.</p>}
      </div>
      <div className="space-y-2">
        <Label htmlFor="confirmPassword">Confirm new password</Label>
        <Input id="confirmPassword" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {mismatch && <p className="text-xs text-destructive">These do not match.</p>}
      </div>
    </FormDialog>
  );
}
