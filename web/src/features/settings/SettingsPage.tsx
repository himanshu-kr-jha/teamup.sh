import { useState } from 'react';
import { User, Bell, Shield, Palette, Database, Loader2, Save } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Label } from '@/shared/components/ui/label';
import { Switch } from '@/shared/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/components/ui/tabs';
import { Separator } from '@/shared/components/ui/separator';
import { useAuth } from '@/features/auth/AuthProvider';
import { api } from '@/shared/api/client';

export function SettingsPage() {
  const { user, refreshUser } = useAuth();
  const [activeTab, setActiveTab] = useState('profile');
  const [saving, setSaving] = useState(false);
  const [formData, setFormData] = useState({
    name: user?.name || '',
    email: user?.email || '',
    timezone: 'UTC',
    locale: 'en',
    notifications: {
      email: true,
      push: true,
      mentions: true,
      messages: true,
    },
    theme: 'system',
  });

  const handleSave = async () => {
    setSaving(true);
    try {
      await api.patch('/api/users/me', formData);
      await refreshUser();
    } catch (err) {
      console.error('Save failed:', err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-screen flex-col bg-background">
      {/* Header */}
      <div className="flex h-16 items-center justify-between px-4 md:px-6 border-b border-border">
        <h1 className="text-2xl font-bold">Settings</h1>
        <Button onClick={handleSave} disabled={saving}>
          {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
          Save Changes
        </Button>
      </div>

      {/* Tabs */}
      <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 overflow-auto">
        <TabsList className="border-b border-border p-4 md:p-6">
          <TabsTrigger value="profile"><User className="mr-2 h-4 w-4" />Profile</TabsTrigger>
          <TabsTrigger value="notifications"><Bell className="mr-2 h-4 w-4" />Notifications</TabsTrigger>
          <TabsTrigger value="security"><Shield className="mr-2 h-4 w-4" />Security</TabsTrigger>
          <TabsTrigger value="appearance"><Palette className="mr-2 h-4 w-4" />Appearance</TabsTrigger>
          <TabsTrigger value="data"><Database className="mr-2 h-4 w-4" />Data</TabsTrigger>
        </TabsList>

        <TabsContent value="profile" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader>
              <CardTitle>Profile Information</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid gap-4 md:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="name">Full Name</Label>
                  <Input id="name" value={formData.name} onChange={(e) => setFormData({...formData, name: e.target.value})} />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="email">Email</Label>
                  <Input id="email" type="email" value={formData.email} onChange={(e) => setFormData({...formData, email: e.target.value})} />
                </div>
              </div>
              <div className="space-y-2">
                <Label htmlFor="timezone">Timezone</Label>
                <Select value={formData.timezone} onValueChange={(v) => setFormData({...formData, timezone: v})}>
                  <SelectTrigger><SelectValue placeholder="Select timezone" /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="UTC">UTC</SelectItem>
                    <SelectItem value="America/New_York">Eastern Time</SelectItem>
                    <SelectItem value="America/Chicago">Central Time</SelectItem>
                    <SelectItem value="America/Denver">Mountain Time</SelectItem>
                    <SelectItem value="America/Los_Angeles">Pacific Time</SelectItem>
                    <SelectItem value="Europe/London">London</SelectItem>
                    <SelectItem value="Europe/Paris">Paris</SelectItem>
                    <SelectItem value="Asia/Tokyo">Tokyo</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="locale">Language</Label>
                <Select value={formData.locale} onValueChange={(v) => setFormData({...formData, locale: v})}>
                  <SelectTrigger><SelectValue placeholder="Select language" /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="en">English</SelectItem>
                    <SelectItem value="es">Spanish</SelectItem>
                    <SelectItem value="fr">French</SelectItem>
                    <SelectItem value="de">German</SelectItem>
                    <SelectItem value="ja">Japanese</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="notifications" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader>
              <CardTitle>Notification Preferences</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              {[
                { key: 'email', label: 'Email Notifications', desc: 'Receive notifications via email' },
                { key: 'push', label: 'Push Notifications', desc: 'Receive push notifications in browser' },
                { key: 'mentions', label: 'Mentions', desc: 'Notify when @mentioned' },
                { key: 'messages', label: 'Direct Messages', desc: 'Notify for new DMs' },
              ].map(({ key, label, desc }) => (
                <div key={key} className="flex items-center justify-between">
                  <div>
                    <p className="font-medium">{label}</p>
                    <p className="text-sm text-muted-foreground">{desc}</p>
                  </div>
                  <Switch
                    checked={formData.notifications[key as keyof typeof formData.notifications]}
                    onCheckedChange={(checked) => setFormData({...formData, notifications: {...formData.notifications, [key]: checked}})}
                  />
                </div>
              ))}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="security" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader>
              <CardTitle>Security</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label>Change Password</Label>
                <Button variant="outline" onClick={() => {}}>Update Password</Button>
              </div>
              <Separator />
              <div className="space-y-2">
                <Label>Two-Factor Authentication</Label>
                <Button variant="outline" onClick={() => {}}>Enable 2FA</Button>
              </div>
              <Separator />
              <div className="space-y-2">
                <Label>Active Sessions</Label>
                <Button variant="outline" onClick={() => {}}>Manage Sessions</Button>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="appearance" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader>
              <CardTitle>Appearance</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label>Theme</Label>
                <Select value={formData.theme} onValueChange={(v) => setFormData({...formData, theme: v})}>
                  <SelectTrigger><SelectValue placeholder="Select theme" /></SelectTrigger>
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

        <TabsContent value="data" className="p-4 md:p-6 space-y-6 max-w-2xl">
          <Card>
            <CardHeader>
              <CardTitle>Data Management</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label>Export Data</Label>
                <Button variant="outline" onClick={() => {}}>Download Data</Button>
              </div>
              <Separator />
              <div className="space-y-2">
                <Label>Delete Account</Label>
                <Button variant="destructive" onClick={() => {}}>Delete Account</Button>
              </div>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
