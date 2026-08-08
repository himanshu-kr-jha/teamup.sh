import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Users, Shield, UsersRound, Trash2, Plus, Copy, UserPlus } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Badge } from '@/shared/components/ui/badge';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/components/ui/tabs';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { FormDialog } from '@/shared/components/FormDialog';
import { orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';
import { usePresence } from '@/features/presence/usePresence';
import { PresenceDot } from '@/features/presence/PresenceDot';

/**
 * Members, roles and teams. The whole API was implemented and tested but had
 * no screen, so the RBAC model was unreachable except by hand.
 */
export function AdminPage() {
  const { user } = useAuth();
  const [tab, setTab] = useState('members');
  const isAdmin = user?.roles?.some((r) => r === 'owner' || r === 'admin') ?? false;

  return (
    <div className="flex h-full flex-col bg-background">
      <div className="flex h-16 items-center justify-between border-b border-border px-4 md:px-6">
        <h1 className="text-2xl font-bold">Organization</h1>
      </div>

      {!isAdmin && (
        <div className="border-b border-border bg-muted/40 px-4 py-2 text-sm text-muted-foreground md:px-6">
          You can see who is here, but only an administrator can make changes.
        </div>
      )}

      <Tabs value={tab} onValueChange={setTab} className="flex-1 overflow-auto">
        <TabsList className="border-b border-border p-4 md:p-6">
          <TabsTrigger value="members"><Users className="mr-2 h-4 w-4" />Members</TabsTrigger>
          <TabsTrigger value="roles"><Shield className="mr-2 h-4 w-4" />Roles</TabsTrigger>
          <TabsTrigger value="teams"><UsersRound className="mr-2 h-4 w-4" />Teams</TabsTrigger>
        </TabsList>

        <TabsContent value="members" className="p-4 md:p-6">
          <MembersTab isAdmin={isAdmin} currentUserId={user?.id} />
        </TabsContent>
        <TabsContent value="roles" className="p-4 md:p-6">
          <RolesTab isAdmin={isAdmin} />
        </TabsContent>
        <TabsContent value="teams" className="p-4 md:p-6">
          <TeamsTab isAdmin={isAdmin} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function MembersTab({ isAdmin, currentUserId }: { isAdmin: boolean; currentUserId?: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [assignFor, setAssignFor] = useState<any | null>(null);
  const [adding, setAdding] = useState(false);
  const [newCredentials, setNewCredentials] = useState<{ email: string; password: string } | null>(null);
  const { statusOf } = usePresence();

  const { data: usersData, isLoading } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });
  const { data: rolesData } = useQuery({ queryKey: ['roles'], queryFn: () => orgApi.listRoles() });
  const users = usersData?.data ?? [];
  const roles = rolesData?.data ?? [];

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['users'] });

  const removeRole = async (userId: string, roleId: string) => {
    try {
      await orgApi.removeRole(userId, roleId);
      refresh();
      toast.success('Role removed.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not remove that role.'));
    }
  };

  const deactivate = async (userId: string) => {
    try {
      await orgApi.deactivateUser(userId);
      refresh();
      toast.success('Member deactivated.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not deactivate that member.'));
    }
  };

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading members…</p>;

  return (
    <>
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0">
          <CardTitle>Members</CardTitle>
          {isAdmin && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <UserPlus className="mr-2 h-4 w-4" />
              Add member
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          {users.length === 0 && <p className="text-sm text-muted-foreground">No members yet.</p>}
          {users.map((u: any) => (
            <div key={u.id} className="flex flex-wrap items-center gap-3 rounded-lg border border-border p-3">
              <PresenceDot status={statusOf(u.id)} />
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{u.name}</p>
                <p className="truncate text-sm text-muted-foreground">{u.email}</p>
              </div>
              <div className="flex flex-wrap gap-1">
                {(u.roles ?? []).map((roleName: string) => {
                  const role = roles.find((r: any) => r.name === roleName);
                  return (
                    <Badge key={roleName} variant="secondary" className="gap-1">
                      {roleName}
                      {isAdmin && role && (
                        <button
                          onClick={() => removeRole(u.id, role.id)}
                          aria-label={`Remove ${roleName} from ${u.name}`}
                          className="ml-1 opacity-60 hover:opacity-100"
                        >
                          ×
                        </button>
                      )}
                    </Badge>
                  );
                })}
                {(u.roles ?? []).length === 0 && (
                  <span className="text-xs text-muted-foreground">no roles</span>
                )}
              </div>
              {isAdmin && (
                <div className="flex gap-2">
                  <Button variant="outline" size="sm" onClick={() => setAssignFor(u)}>
                    Assign role
                  </Button>
                  {u.id !== currentUserId && (
                    <Button variant="ghost" size="sm" onClick={() => deactivate(u.id)}>
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  )}
                </div>
              )}
            </div>
          ))}
        </CardContent>
      </Card>

      <AddMemberDialog
        open={adding}
        roles={roles}
        onOpenChange={setAdding}
        onAdded={(credentials) => {
          refresh();
          setNewCredentials(credentials);
        }}
      />

      <TemporaryPasswordDialog
        credentials={newCredentials}
        onClose={() => setNewCredentials(null)}
      />

      <AssignRoleDialog
        member={assignFor}
        roles={roles}
        onOpenChange={(open) => !open && setAssignFor(null)}
        onAssigned={refresh}
      />
    </>
  );
}

/**
 * Adding somebody is the one thing the Organization screen could not do, which
 * left every installation a single-user demo. No email is sent: SMTP is
 * optional in v1, so the administrator passes the password on themselves.
 */
function AddMemberDialog({
  open,
  roles,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  roles: any[];
  onOpenChange: (open: boolean) => void;
  onAdded: (credentials: { email: string; password: string }) => void;
}) {
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [role, setRole] = useState('');

  const reset = () => {
    setName('');
    setEmail('');
    setRole('');
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
      title="Add member"
      description="Creates an account they can sign in with straight away."
      submitLabel="Add member"
      disabledReason={!name.trim() || !email.trim() ? 'Enter a name and an email address.' : undefined}
      onSubmit={async () => {
        const res = await orgApi.addMember({
          name: name.trim(),
          email: email.trim(),
          role: role || undefined,
        });
        const created = { email: email.trim(), password: res.data.temporaryPassword };
        reset();
        onAdded(created);
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="memberName">Name</Label>
        <Input
          id="memberName"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Ada Lovelace"
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="memberEmail">Email</Label>
        <Input
          id="memberEmail"
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="ada@example.com"
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="memberRole">Role</Label>
        <Select value={role} onValueChange={setRole}>
          <SelectTrigger id="memberRole"><SelectValue placeholder="member" /></SelectTrigger>
          <SelectContent>
            {roles.map((r: any) => (
              <SelectItem key={r.id} value={r.name}>{r.name}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </FormDialog>
  );
}

/**
 * The password is stored hashed and cannot be recovered, so this is the only
 * time anybody will ever see it. Says so plainly rather than letting an
 * administrator close the dialog and find out later.
 */
function TemporaryPasswordDialog({
  credentials,
  onClose,
}: {
  credentials: { email: string; password: string } | null;
  onClose: () => void;
}) {
  const toast = useToast();

  return (
    <Dialog open={!!credentials} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Member added</DialogTitle>
          <DialogDescription>
            Pass these on to {credentials?.email}. They will be asked to choose their
            own password the first time they sign in.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor="temporaryPassword">Temporary password</Label>
          <div className="flex gap-2">
            <Input id="temporaryPassword" readOnly value={credentials?.password ?? ''} className="font-mono" />
            <Button
              type="button"
              variant="outline"
              size="icon"
              aria-label="Copy password"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(credentials?.password ?? '');
                  toast.success('Password copied.');
                } catch {
                  toast.error('Could not copy — select it and copy by hand.');
                }
              }}
            >
              <Copy className="h-4 w-4" />
            </Button>
          </div>
          <p className="text-sm text-destructive">
            This will not be shown again. Copy it before closing this dialog.
          </p>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function AssignRoleDialog({
  member,
  roles,
  onOpenChange,
  onAssigned,
}: {
  member: any | null;
  roles: any[];
  onOpenChange: (open: boolean) => void;
  onAssigned: () => void;
}) {
  const [roleId, setRoleId] = useState('');
  const toast = useToast();

  return (
    <FormDialog
      open={!!member}
      onOpenChange={(o) => {
        if (!o) setRoleId('');
        onOpenChange(o);
      }}
      title={member ? `Assign a role to ${member.name}` : 'Assign a role'}
      description="Roles decide what someone is allowed to do. Changes take effect when their session next refreshes."
      submitLabel="Assign role"
      disabledReason={!roleId ? 'Pick a role' : undefined}
      onSubmit={async () => {
        await orgApi.assignRole(member.id, roleId);
        onAssigned();
        toast.success('Role assigned.');
        setRoleId('');
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="roleSelect">Role</Label>
        <Select value={roleId} onValueChange={setRoleId}>
          <SelectTrigger id="roleSelect"><SelectValue placeholder="Choose a role" /></SelectTrigger>
          <SelectContent>
            {roles.map((r: any) => (
              <SelectItem key={r.id} value={r.id}>{r.name}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </FormDialog>
  );
}

function RolesTab({ isAdmin }: { isAdmin: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [permissions, setPermissions] = useState('');

  const { data, isLoading } = useQuery({ queryKey: ['roles'], queryFn: () => orgApi.listRoles() });
  const roles = data?.data ?? [];

  const remove = async (id: string) => {
    try {
      await orgApi.deleteRole(id);
      queryClient.invalidateQueries({ queryKey: ['roles'] });
      toast.success('Role deleted.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not delete that role.'));
    }
  };

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading roles…</p>;

  return (
    <>
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Roles</CardTitle>
          {isAdmin && (
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="mr-1 h-4 w-4" />New role
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          {roles.map((r: any) => (
            <div key={r.id} className="flex items-start gap-3 rounded-lg border border-border p-3">
              <div className="min-w-0 flex-1">
                <p className="font-medium">
                  {r.name}
                  {r.isSystem && <Badge variant="secondary" className="ml-2">built in</Badge>}
                </p>
                <p className="truncate text-sm text-muted-foreground">
                  {(r.permissions ?? []).join(', ') || 'no permissions'}
                </p>
              </div>
              {isAdmin && !r.isSystem && (
                <Button variant="ghost" size="sm" onClick={() => remove(r.id)}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              )}
            </div>
          ))}
        </CardContent>
      </Card>

      <FormDialog
        open={createOpen}
        onOpenChange={(o) => {
          if (!o) {
            setName('');
            setPermissions('');
          }
          setCreateOpen(o);
        }}
        title="New role"
        description="Built-in roles cannot be changed; create your own for anything else."
        submitLabel="Create role"
        disabledReason={!name.trim() ? 'Give the role a name' : undefined}
        onSubmit={async () => {
          await orgApi.createRole({
            name: name.trim(),
            permissions: permissions.split(',').map((p) => p.trim()).filter(Boolean),
          });
          queryClient.invalidateQueries({ queryKey: ['roles'] });
          setName('');
          setPermissions('');
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="roleName">Name</Label>
          <Input id="roleName" value={name} onChange={(e) => setName(e.target.value)} placeholder="auditor" />
        </div>
        <div className="space-y-2">
          <Label htmlFor="rolePermissions">Permissions</Label>
          <Input
            id="rolePermissions"
            value={permissions}
            onChange={(e) => setPermissions(e.target.value)}
            placeholder="tasks:read, reports:read"
          />
          <p className="text-xs text-muted-foreground">Comma separated.</p>
        </div>
      </FormDialog>
    </>
  );
}

function TeamsTab({ isAdmin }: { isAdmin: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [addTo, setAddTo] = useState<any | null>(null);
  const [memberId, setMemberId] = useState('');
  const [managerFor, setManagerFor] = useState<any | null>(null);
  const [managerId, setManagerId] = useState('');

  const { data, isLoading } = useQuery({ queryKey: ['teams'], queryFn: () => orgApi.listTeams() });
  const { data: usersData } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });
  const teams = data?.data ?? [];
  const users = usersData?.data ?? [];

  const remove = async (id: string) => {
    try {
      await orgApi.deleteTeam(id);
      queryClient.invalidateQueries({ queryKey: ['teams'] });
      toast.success('Team deleted.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not delete that team.'));
    }
  };

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading teams…</p>;

  return (
    <>
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Teams</CardTitle>
          {isAdmin && (
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="mr-1 h-4 w-4" />New team
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          {teams.length === 0 && <p className="text-sm text-muted-foreground">No teams yet.</p>}
          {teams.map((t: any) => (
            <div key={t.id} className="flex items-center gap-3 rounded-lg border border-border p-3">
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{t.name}</p>
                <p className="truncate text-sm text-muted-foreground">
                  {t.memberCount} {t.memberCount === 1 ? 'member' : 'members'}
                  {t.description ? ` · ${t.description}` : ''}
                </p>
                <p className="truncate text-sm text-muted-foreground">
                  Manager: {users.find((u: any) => u.id === t.managerId)?.name ?? 'nobody yet'}
                </p>
              </div>
              {isAdmin && (
                <div className="flex gap-2">
                  <Button variant="outline" size="sm" onClick={() => setManagerFor(t)}>Set manager</Button>
                  <Button variant="outline" size="sm" onClick={() => setAddTo(t)}>Add member</Button>
                  <Button variant="ghost" size="sm" onClick={() => remove(t.id)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              )}
            </div>
          ))}
        </CardContent>
      </Card>

      <FormDialog
        open={!!managerFor}
        onOpenChange={(o) => {
          if (!o) {
            setManagerFor(null);
            setManagerId('');
          }
        }}
        title={`Manager of ${managerFor?.name ?? 'team'}`}
        description="The manager can assign work to anybody on this team, or on a team beneath it."
        submitLabel="Save manager"
        disabledReason={!managerId ? 'Choose somebody.' : undefined}
        onSubmit={async () => {
          await orgApi.updateTeam(managerFor.id, { managerId });
          queryClient.invalidateQueries({ queryKey: ['teams'] });
          setManagerFor(null);
          setManagerId('');
          toast.success('Manager set.');
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="teamManager">Manager</Label>
          <Select value={managerId} onValueChange={setManagerId}>
            <SelectTrigger id="teamManager"><SelectValue placeholder="Choose somebody" /></SelectTrigger>
            <SelectContent>
              {users.map((u: any) => (
                <SelectItem key={u.id} value={u.id}>{u.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </FormDialog>

      <FormDialog
        open={createOpen}
        onOpenChange={(o) => {
          if (!o) {
            setName('');
            setDescription('');
          }
          setCreateOpen(o);
        }}
        title="New team"
        submitLabel="Create team"
        disabledReason={!name.trim() ? 'Give the team a name' : undefined}
        onSubmit={async () => {
          await orgApi.createTeam({ name: name.trim(), description: description.trim() || undefined });
          queryClient.invalidateQueries({ queryKey: ['teams'] });
          setName('');
          setDescription('');
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="teamName">Name</Label>
          <Input id="teamName" value={name} onChange={(e) => setName(e.target.value)} placeholder="Platform" />
        </div>
        <div className="space-y-2">
          <Label htmlFor="teamDescription">Description (optional)</Label>
          <Input id="teamDescription" value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
      </FormDialog>

      <FormDialog
        open={!!addTo}
        onOpenChange={(o) => {
          if (!o) {
            setAddTo(null);
            setMemberId('');
          }
        }}
        title={addTo ? `Add someone to ${addTo.name}` : 'Add a member'}
        submitLabel="Add member"
        disabledReason={!memberId ? 'Pick a member' : undefined}
        onSubmit={async () => {
          await orgApi.addTeamMember(addTo.id, memberId);
          queryClient.invalidateQueries({ queryKey: ['teams'] });
          setAddTo(null);
          setMemberId('');
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="teamMember">Member</Label>
          <Select value={memberId} onValueChange={setMemberId}>
            <SelectTrigger id="teamMember"><SelectValue placeholder="Choose someone" /></SelectTrigger>
            <SelectContent>
              {users.map((u: any) => (
                <SelectItem key={u.id} value={u.id}>{u.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </FormDialog>
    </>
  );
}
