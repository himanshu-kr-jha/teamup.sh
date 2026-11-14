import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Users,
  Shield,
  UsersRound,
  Trash2,
  Plus,
  Copy,
  UserPlus,
  Download,
  Upload,
  CornerDownRight,
  Mail,
  Palette,
  NotebookPen,
  FolderGit2,
  Plug,
  Gauge,
} from 'lucide-react';
import { MailSettingsTab, mailProblemText } from './MailSettingsTab';
import { AppearanceTab } from './AppearanceTab';
import { NotetakerTab } from './NotetakerTab';
import { ProjectsTab } from './ProjectsTab';
import { IntegrationsTab } from './IntegrationsTab';
import { SystemTab } from './SystemTab';
import { RolesTab, roleSummary, usePermissionCatalog, withinReach } from './RolesTab';
import { PERMS, can } from '@/shared/permissions';
import { UserAvatar } from '@/shared/components/UserAvatar';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Textarea } from '@/shared/components/ui/textarea';
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
import { PeoplePicker, addEach, addLabel } from '@/shared/components/PeoplePicker';
import { orgApi, tasksApi } from '@/shared/api/client';
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
  // Back from GitHub, the Integrations tab says how it went.
  const [params, setParams] = useSearchParams();
  const githubOutcome = params.get('github');
  const bitbucketOutcome = params.get('bitbucket');
  const [tab, setTab] = useState(() => params.get('tab') ?? (githubOutcome || bitbucketOutcome ? 'integrations' : 'members'));
  const may = {
    settings: can(user, PERMS.orgSettings),
    members: can(user, PERMS.members),
    roles: can(user, PERMS.roles),
    teams: can(user, PERMS.teams),
    notes: can(user, PERMS.notesSettings),
  };
  const mayAnything = Object.values(may).some(Boolean);
  // Somebody who runs a team can manage that team without administering the
  // organization, so the notice has to allow for it.
  const { data: managedData } = useQuery({
    queryKey: ['managed-teams'],
    queryFn: () => tasksApi.managedTeams(),
  });
  const managedTeamIds = new Set<string>((managedData?.data ?? []).map((t: any) => String(t.id)));

  return (
    <div className="flex h-full flex-col bg-background">
      <div className="flex h-16 items-center justify-between border-b border-border px-4 md:px-6">
        <h1 className="text-2xl font-bold">Organization</h1>
      </div>

      {!mayAnything && (
        <div className="border-b border-border bg-muted/40 px-4 py-2 text-sm text-muted-foreground md:px-6">
          {managedTeamIds.size > 0
            ? 'You can see who is here, and manage the teams you run.'
            : 'You can see who is here, but your role does not let you change anything.'}
        </div>
      )}

      <Tabs value={tab} onValueChange={setTab} className="flex-1 overflow-y-auto overflow-x-hidden">
        {/* Four tabs are wider than a phone, so the row scrolls rather than the page. */}
        <TabsList className="flex max-w-full justify-start overflow-x-auto border-b border-border p-4 scrollbar-hide md:inline-flex md:p-6">
          <TabsTrigger value="members"><Users className="mr-2 h-4 w-4" />Members</TabsTrigger>
          <TabsTrigger value="roles"><Shield className="mr-2 h-4 w-4" />Roles</TabsTrigger>
          <TabsTrigger value="teams"><UsersRound className="mr-2 h-4 w-4" />Teams</TabsTrigger>
          {may.settings && <TabsTrigger value="email"><Mail className="mr-2 h-4 w-4" />Email</TabsTrigger>}
          {may.settings && <TabsTrigger value="appearance"><Palette className="mr-2 h-4 w-4" />Appearance</TabsTrigger>}
          {may.notes && <TabsTrigger value="notes"><NotebookPen className="mr-2 h-4 w-4" />Meeting notes</TabsTrigger>}
          <TabsTrigger value="projects"><FolderGit2 className="mr-2 h-4 w-4" />Projects</TabsTrigger>
          {may.settings && <TabsTrigger value="integrations"><Plug className="mr-2 h-4 w-4" />Integrations</TabsTrigger>}
          {may.settings && <TabsTrigger value="system"><Gauge className="mr-2 h-4 w-4" />System</TabsTrigger>}
        </TabsList>

        <TabsContent value="members" className="p-4 md:p-6">
          <MembersTab
            canMembers={may.members}
            canRoles={may.roles}
            isOwner={user?.roles?.includes('owner') ?? false}
            currentUserId={user?.id}
            held={user?.permissions}
          />
        </TabsContent>
        <TabsContent value="roles" className="p-4 md:p-6">
          <RolesTab canManage={may.roles} />
        </TabsContent>
        <TabsContent value="teams" className="p-4 md:p-6">
          <TeamsTab isAdmin={may.teams} managedTeamIds={managedTeamIds} />
        </TabsContent>
        {may.settings && (
          <TabsContent value="email" className="p-4 md:p-6">
            <MailSettingsTab />
          </TabsContent>
        )}
        {may.settings && (
          <TabsContent value="appearance" className="p-4 md:p-6">
            <AppearanceTab />
          </TabsContent>
        )}
        {may.notes && (
          <TabsContent value="notes" className="p-4 md:p-6">
            <NotetakerTab />
          </TabsContent>
        )}
        <TabsContent value="projects" className="p-4 md:p-6">
          <ProjectsTab canManage={may.settings} userId={user?.id} />
        </TabsContent>
        {may.settings && (
          <TabsContent value="integrations" className="p-4 md:p-6">
            <IntegrationsTab
              outcome={githubOutcome}
              bitbucketOutcome={bitbucketOutcome}
              onOutcomeSeen={() =>
                setParams(
                  (current) => {
                    const next = new URLSearchParams(current);
                    next.delete('github');
                    next.delete('bitbucket');
                    return next;
                  },
                  { replace: true }
                )
              }
            />
          </TabsContent>
        )}
        {may.settings && (
          <TabsContent value="system" className="p-4 md:p-6">
            <SystemTab />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}

function MembersTab({
  canMembers,
  canRoles,
  isOwner,
  currentUserId,
  held,
}: {
  canMembers: boolean;
  canRoles: boolean;
  isOwner: boolean;
  currentUserId?: string;
  /** What the person looking holds, so they are offered only roles within reach. */
  held?: string[];
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [assignFor, setAssignFor] = useState<any | null>(null);
  const [resetFor, setResetFor] = useState<any | null>(null);
  const [adding, setAdding] = useState(false);
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<{ outcomes: ImportOutcome[]; mailProblem?: string } | null>(null);
  const [newCredentials, setNewCredentials] = useState<
    { name: string; email: string; password: string; emailed: boolean; mailProblem?: string; reset?: boolean } | null
  >(null);
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
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0">
          <CardTitle>Members</CardTitle>
          {canMembers && (
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setImporting(true)}>
                <Upload className="mr-2 h-4 w-4" />
                Import CSV
              </Button>
              <Button size="sm" onClick={() => setAdding(true)}>
                <UserPlus className="mr-2 h-4 w-4" />
                Add member
              </Button>
            </div>
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          {users.length === 0 && <p className="text-sm text-muted-foreground">No members yet.</p>}
          {users.map((u: any) => (
            <div key={u.id} className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-border p-3">
              <div className="relative shrink-0">
                <UserAvatar name={u.name} src={u.avatarUrl} className="h-9 w-9" />
                <PresenceDot status={statusOf(u.id)} className="absolute -bottom-0.5 -right-0.5" />
              </div>
              <div className="min-w-[10rem] flex-1 basis-full sm:basis-auto">
                <p className="truncate font-medium">{u.name}</p>
                <p className="truncate text-sm text-muted-foreground">{u.email}</p>
              </div>
              <div className="flex flex-wrap gap-1">
                {(u.roles ?? []).map((roleName: string) => {
                  const role = roles.find((r: any) => r.name === roleName);
                  return (
                    <Badge key={roleName} variant="secondary" className="gap-1">
                      {roleName}
                      {canRoles && role && withinReach(role, held) && (
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
              {(canMembers || canRoles) && (
                <div className="flex flex-wrap gap-2">
                  {canRoles && (
                    <Button variant="outline" size="sm" onClick={() => setAssignFor(u)}>
                      Assign role
                    </Button>
                  )}
                  {canMembers && u.id !== currentUserId && (isOwner || !(u.roles ?? []).includes('owner')) && (
                    <Button variant="outline" size="sm" onClick={() => setResetFor(u)}>
                      Reset password
                    </Button>
                  )}
                  {canMembers && u.id !== currentUserId && (
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
        roles={roles.filter((r: any) => withinReach(r, held))}
        onOpenChange={setAdding}
        onAdded={(credentials) => {
          refresh();
          setNewCredentials(credentials);
        }}
      />

      <FormDialog
        open={!!resetFor}
        onOpenChange={(o) => !o && setResetFor(null)}
        title="Reset password"
        description={`${resetFor?.name} will be signed out everywhere and their current password will stop working. You get a temporary password to pass on.`}
        submitLabel="Reset password"
        onSubmit={async () => {
          const res = await orgApi.resetPassword(resetFor.id);
          setNewCredentials({
            name: resetFor.name,
            email: resetFor.email,
            password: res.data.temporaryPassword,
            emailed: false,
            reset: true,
          });
        }}
      >
        {null}
      </FormDialog>

      <TemporaryPasswordDialog
        credentials={newCredentials}
        onClose={() => setNewCredentials(null)}
      />

      <BulkImportDialog
        open={importing}
        onOpenChange={setImporting}
        onImported={(outcomes, mailProblem) => {
          refresh();
          setImportResult({ outcomes, mailProblem });
        }}
      />

      <ImportResultDialog
        outcomes={importResult?.outcomes ?? null}
        mailProblem={importResult?.mailProblem}
        onClose={() => setImportResult(null)}
      />

      <AssignRoleDialog
        member={assignFor}
        roles={roles.filter((r: any) => withinReach(r, held) && !(assignFor?.roles ?? []).includes(r.name))}
        onOpenChange={(open) => !open && setAssignFor(null)}
        onAssigned={refresh}
      />
    </>
  );
}

/**
 * Adding somebody is the one thing the Organization screen could not do, which
 * left every installation a single-user demo. With a mail server set up under
 * Email they are sent their details; without one the administrator passes the
 * password on themselves.
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
  onAdded: (credentials: { name: string; email: string; password: string; emailed: boolean; mailProblem?: string }) => void;
}) {
  const { data: mailData } = useQuery({ queryKey: ['mail-settings'], queryFn: () => orgApi.getMail(), enabled: open });
  const mailReady = mailData?.data.configured;
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
      description={
        mailReady
          ? 'Creates an account they can sign in with straight away, and emails them their sign-in details.'
          : 'Creates an account they can sign in with straight away. No mail server is set up, so you will pass the password on yourself.'
      }
      submitLabel="Add member"
      disabledReason={!name.trim() || !email.trim() ? 'Enter a name and an email address.' : undefined}
      onSubmit={async () => {
        const res = await orgApi.addMember({
          name: name.trim(),
          email: email.trim(),
          role: role || undefined,
        });
        const created = {
          name: name.trim(),
          email: email.trim(),
          password: res.data.temporaryPassword,
          emailed: !!res.data.emailed,
          mailProblem: res.data.mailProblem,
        };
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
              <SelectItem key={r.id} value={r.name}>
                <RoleOption role={r} />
              </SelectItem>
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
  credentials: { name: string; email: string; password: string; emailed: boolean; mailProblem?: string; reset?: boolean } | null;
  onClose: () => void;
}) {
  const toast = useToast();

  // A file the administrator can pass on directly. Built in the browser
  // because this password exists nowhere else — it is hashed on the server and
  // cannot be fetched again.
  const downloadCredentials = () => {
    if (!credentials) return;
    const signIn = `${window.location.origin}/login`;
    const rows = [
      ['Name', 'Email', 'Temporary password', 'Sign in at'],
      [credentials.name, credentials.email, credentials.password, signIn],
    ];
    const csv = rows
      .map((row) => row.map((cell) => `"${String(cell).replace(/"/g, '""')}"`).join(','))
      .join('\r\n');

    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `teamup-${credentials.email.replace(/[^a-z0-9]+/gi, '-')}.csv`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <Dialog open={!!credentials} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{credentials?.reset ? 'Password reset' : 'Member added'}</DialogTitle>
          <DialogDescription>
            {credentials?.emailed
              ? `Sent to ${credentials?.email}. They will be asked to choose their own password the first time they sign in.`
              : `Pass these on to ${credentials?.email}. They will be asked to choose their own password the next time they sign in.`}
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
                  toast.error('Could not copy. Select it and copy it by hand.');
                }
              }}
            >
              <Copy className="h-4 w-4" />
            </Button>
          </div>
          <p className="text-sm text-destructive">
            This will not be shown again. Copy it or download it before closing.
          </p>
          {!credentials?.emailed && !credentials?.reset && (
            <p role="status" className="text-sm text-muted-foreground">
              {mailProblemText(credentials?.mailProblem)} Pass these on to them yourself.
            </p>
          )}
        </div>
        <DialogFooter className="gap-2 sm:justify-between">
          <Button variant="outline" onClick={downloadCredentials}>
            <Download className="mr-2 h-4 w-4" />
            Download as CSV
          </Button>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type ImportOutcome = {
  row: number;
  name: string;
  email: string;
  status: 'created' | 'duplicate' | 'invalid' | 'failed';
  message?: string;
  temporaryPassword?: string;
  emailed?: boolean;
};

/**
 * Onboarding a 50-person company meant opening the add-member dialog 49 times
 * and copying 49 passwords out of it one at a time. This does the whole
 * spreadsheet in one go and hands back every password at the end.
 */
function BulkImportDialog({
  open,
  onOpenChange,
  onImported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onImported: (outcomes: ImportOutcome[], mailProblem?: string) => void;
}) {
  const [csv, setCsv] = useState('');
  const [fileName, setFileName] = useState('');

  const reset = () => {
    setCsv('');
    setFileName('');
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset();
        onOpenChange(o);
      }}
      title="Import members"
      description="One row per person. Everybody gets an account and a temporary password straight away."
      submitLabel="Import"
      disabledReason={!csv.trim() ? 'Choose a file, or paste the rows in.' : undefined}
      onSubmit={async () => {
        const res = await orgApi.importMembers(csv);
        const outcomes = res.data.outcomes ?? [];
        reset();
        onImported(outcomes, res.data.mailProblem);
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="importFile">CSV file</Label>
        <Input
          id="importFile"
          type="file"
          accept=".csv,text/csv"
          onChange={async (e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            setFileName(file.name);
            // Read here rather than posting the file itself: the browser
            // already has it, and it lets the administrator see and correct
            // the rows before anything is created.
            setCsv(await file.text());
          }}
        />
        {fileName && <p className="text-xs text-muted-foreground">Loaded {fileName}.</p>}
      </div>
      <div className="space-y-2">
        <Label htmlFor="importCsv">Rows</Label>
        <Textarea
          id="importCsv"
          rows={8}
          className="font-mono text-xs"
          value={csv}
          onChange={(e) => setCsv(e.target.value)}
          placeholder={'name,email,role,team\nAda Lovelace,ada@example.com,member,Platform'}
        />
        <p className="text-xs text-muted-foreground">
          The first row names the columns. <span className="font-mono">name</span> and{' '}
          <span className="font-mono">email</span> are required;{' '}
          <span className="font-mono">role</span> and <span className="font-mono">team</span> are
          optional. A row that fails is reported on its own — the rest still go through.
        </p>
      </div>
    </FormDialog>
  );
}

/**
 * What happened to each row, and the passwords. Same reasoning as the
 * single-member dialog: the passwords are hashed on the server and exist
 * nowhere else, so this is the only chance to keep them.
 */
function ImportResultDialog({
  outcomes,
  mailProblem,
  onClose,
}: {
  outcomes: ImportOutcome[] | null;
  mailProblem?: string;
  onClose: () => void;
}) {
  const created = (outcomes ?? []).filter((o) => o.status === 'created');
  const emailed = created.filter((o) => o.emailed).length;
  const failed = (outcomes ?? []).filter((o) => o.status !== 'created');

  const downloadCredentials = () => {
    const signIn = `${window.location.origin}/login`;
    const rows = [
      ['Name', 'Email', 'Temporary password', 'Sign in at'],
      ...created.map((o) => [o.name, o.email, o.temporaryPassword ?? '', signIn]),
    ];
    const csv = rows
      .map((row) => row.map((cell) => `"${String(cell).replace(/"/g, '""')}"`).join(','))
      .join('\r\n');

    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `teamup-new-members-${new Date().toISOString().slice(0, 10)}.csv`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <Dialog open={!!outcomes} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {created.length} added{failed.length > 0 ? `, ${failed.length} not` : ''}
          </DialogTitle>
          <DialogDescription>
            {emailed > 0 && `${emailed} of ${created.length} were emailed their sign-in details. `}
            Download the passwords before closing. They are stored hashed and cannot be shown again.
          </DialogDescription>
          {created.length > emailed && (
            <p role="status" className="text-sm text-muted-foreground">
              {mailProblemText(mailProblem)} Pass the rest on yourself.
            </p>
          )}
        </DialogHeader>

        <ul className="max-h-72 space-y-1 overflow-y-auto text-sm">
          {(outcomes ?? []).map((o) => (
            <li key={`${o.row}-${o.email}`} className="flex items-start gap-2 rounded-md border border-border p-2">
              <span className="w-8 shrink-0 text-xs text-muted-foreground">{o.row}</span>
              <div className="min-w-0 flex-1">
                <p className="truncate">{o.email || o.name || '(blank row)'}</p>
                {o.status === 'created' ? (
                  <p className="truncate font-mono text-xs text-muted-foreground">
                    {o.temporaryPassword}
                  </p>
                ) : (
                  <p className="truncate text-xs text-destructive">{o.message || o.status}</p>
                )}
              </div>
              {o.status === 'created' && o.emailed && <Badge variant="outline">emailed</Badge>}
              <Badge variant={o.status === 'created' ? 'secondary' : 'destructive'}>{o.status}</Badge>
            </li>
          ))}
        </ul>

        <DialogFooter className="gap-2 sm:justify-between">
          <Button variant="outline" onClick={downloadCredentials} disabled={created.length === 0}>
            <Download className="mr-2 h-4 w-4" />
            Download passwords
          </Button>
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
      description="A role adds to what somebody may do, straight away; they may need to reload the page to see it. Only roles within your own permissions are offered."
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
              <SelectItem key={r.id} value={r.id}>
                <RoleOption role={r} />
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </FormDialog>
  );
}


/**
 * Radix will not accept an empty string as a Select value, and "no parent" is
 * a real choice rather than the absence of one, so it needs a stand-in.
 */
const TOP_LEVEL = '__top__';

/**
 * Teams come back as a flat array carrying parentId. Rendering that array
 * straight showed every team at the same level, so the org chart the database
 * has always stored — and walks recursively to work out who manages whom —
 * was invisible: the tree always looked one deep.
 *
 * Returns the teams in depth-first order with the depth to indent by.
 */
function asTree(teams: any[]): Array<{ team: any; depth: number }> {
  const byId = new Map<string, any>(teams.map((t) => [String(t.id), t]));
  const children = new Map<string, any[]>();
  for (const t of teams) {
    // A parent that is not in this list — deleted, or belonging to somebody
    // else — is treated as no parent, so no team can drop off the screen.
    const key = t.parentId && byId.has(String(t.parentId)) ? String(t.parentId) : '';
    children.set(key, [...(children.get(key) ?? []), t]);
  }

  const out: Array<{ team: any; depth: number }> = [];
  const placed = new Set<string>();
  const walk = (parentKey: string, depth: number) => {
    for (const t of children.get(parentKey) ?? []) {
      const id = String(t.id);
      // The server refuses to create a cycle, but the browser must not hang if
      // one ever reaches it anyway.
      if (placed.has(id)) continue;
      placed.add(id);
      out.push({ team: t, depth });
      walk(id, depth + 1);
    }
  };
  walk('', 0);
  for (const t of teams) if (!placed.has(String(t.id))) out.push({ team: t, depth: 0 });
  return out;
}

/**
 * Where a team may legally be filed: anywhere except itself and the teams
 * below it, and — for somebody who is not an administrator — only among the
 * teams they run. Mirrors the checks the server makes, so an impossible move
 * is never offered rather than being offered and then refused.
 *
 * This is convenience, not enforcement. The server decides, against the tree
 * as it stands at the moment of the write; this list is only as fresh as the
 * last time the teams were fetched.
 *
 * @param within  the ids the caller is allowed to file under at all, or
 *                undefined for an administrator, who may use any of them.
 */
function eligibleParents(teams: any[], teamId?: string, within?: Set<string>): any[] {
  const banned = new Set<string>(teamId ? [String(teamId)] : []);
  if (teamId) {
    for (let pass = 0; pass < teams.length; pass++) {
      let grew = false;
      for (const t of teams) {
        const id = String(t.id);
        if (!banned.has(id) && t.parentId && banned.has(String(t.parentId))) {
          banned.add(id);
          grew = true;
        }
      }
      if (!grew) break;
    }
  }
  return teams.filter((t) => !banned.has(String(t.id)) && (!within || within.has(String(t.id))));
}

function TeamsTab({
  isAdmin,
  managedTeamIds,
}: {
  isAdmin: boolean;
  /** Teams this person runs, which they may add to without being an admin. */
  managedTeamIds: Set<string>;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  // The top level is only an option for an administrator, so a lead starts
  // with nothing chosen rather than with a choice they are not allowed.
  const [newParentId, setNewParentId] = useState(isAdmin ? TOP_LEVEL : '');
  const [addTo, setAddTo] = useState<any | null>(null);
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [managerFor, setManagerFor] = useState<any | null>(null);
  const [movingTeam, setMovingTeam] = useState<any | null>(null);
  const [moveToId, setMoveToId] = useState(isAdmin ? TOP_LEVEL : '');
  const [viewing, setViewing] = useState<any | null>(null);
  const [managerId, setManagerId] = useState('');

  const { data, isLoading } = useQuery({ queryKey: ['teams'], queryFn: () => orgApi.listTeams() });
  const { data: usersData } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });
  const teams = data?.data ?? [];
  const users = usersData?.data ?? [];
  const tree = asTree(teams);
  // Who is on the team being added to, so the list offers only who is not.
  const { data: addToMembersData } = useQuery({
    queryKey: ['team-members', addTo?.id],
    queryFn: () => orgApi.listTeamMembers(addTo.id),
    enabled: !!addTo,
  });
  const onTeam = new Set((addToMembersData?.data ?? []).map((m: any) => m.id ?? m.userId));
  const joinable = users.filter((u: any) => !onTeam.has(u.id));

  // An administrator may file a team anywhere; a lead only underneath one they
  // run. Passing undefined rather than the whole set is what says "no limit".
  const canParent = isAdmin ? undefined : managedTeamIds;
  // A lead reshapes their own subtree, so they get the create and move
  // affordances too — but only once they actually run something.
  const canCreate = isAdmin || managedTeamIds.size > 0;
  // Whether there is anywhere this team could go. For a lead the answer is no
  // for the team at the top of their subtree: every parent they may choose
  // sits beneath it, so moving it would be a cycle. That is the server's rule
  // as well, so the button is simply not offered.
  const canMove = (t: any) =>
    isAdmin || (managedTeamIds.has(String(t.id)) && eligibleParents(teams, String(t.id), canParent).length > 0);
  // An administrator's new team defaults to the top level; a lead has no
  // default, because the top level is not theirs to choose.
  const defaultParentChoice = isAdmin ? TOP_LEVEL : '';

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
          {canCreate && (
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="mr-1 h-4 w-4" />New team
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          {teams.length === 0 && <p className="text-sm text-muted-foreground">No teams yet.</p>}
          {tree.map(({ team: t, depth }) => (
            <div
              key={t.id}
              // Indented, and hung off a rule, so the shape of the organization
              // is legible at a glance instead of being a flat list.
              style={{ marginLeft: `${Math.min(depth, 8) * 1.5}rem` }}
              className={`flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-border p-3 ${
                depth > 0 ? 'border-l-4 border-l-primary/30' : ''
              }`}
            >
              <div className="min-w-[10rem] flex-1 basis-full sm:basis-auto">
                <p className="truncate font-medium">{t.name}</p>
                <p className="truncate text-sm text-muted-foreground">
                  {t.memberCount} {t.memberCount === 1 ? 'member' : 'members'}
                  {t.description ? ` · ${t.description}` : ''}
                </p>
                <p className="truncate text-sm text-muted-foreground">
                  Manager: {users.find((u: any) => u.id === t.managerId)?.name ?? 'nobody yet'}
                  {t.parentId
                    ? ` · under ${teams.find((p: any) => p.id === t.parentId)?.name ?? 'another team'}`
                    : ''}
                </p>
              </div>
              {(isAdmin || managedTeamIds.has(t.id)) && (
                <div className="flex gap-2">
                  {isAdmin && (
                    <Button variant="outline" size="sm" onClick={() => setManagerFor(t)}>Set manager</Button>
                  )}
                  {canMove(t) && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setMovingTeam(t);
                        // A lead's current parent is often above their reach,
                        // and the top level is never theirs to choose, so they
                        // start with nothing selected and have to pick.
                        const current = t.parentId ? String(t.parentId) : TOP_LEVEL;
                        setMoveToId(isAdmin || managedTeamIds.has(current) ? current : '');
                      }}
                    >
                      <CornerDownRight className="mr-1 h-4 w-4" />
                      Move
                    </Button>
                  )}
                  <Button variant="outline" size="sm" onClick={() => setAddTo(t)}>Add member</Button>
                  <Button variant="outline" size="sm" onClick={() => setViewing(t)}>Members</Button>
                  {isAdmin && (
                    <Button variant="ghost" size="sm" onClick={() => remove(t.id)}>
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  )}
                </div>
              )}
            </div>
          ))}
        </CardContent>
      </Card>

      <FormDialog
        open={!!movingTeam}
        onOpenChange={(o) => {
          if (!o) {
            setMovingTeam(null);
            setMoveToId(defaultParentChoice);
          }
        }}
        title={movingTeam ? `Where does ${movingTeam.name} sit?` : 'Move team'}
        description="Whoever runs the team above also runs this one, and everything beneath it."
        submitLabel="Move team"
        disabledReason={!moveToId ? 'Choose where it sits.' : undefined}
        onSubmit={async () => {
          await orgApi.updateTeam(movingTeam.id, {
            // An empty string is what tells the server to lift the team to the
            // top level; leaving the field out would mean "don't touch it".
            parentId: moveToId === TOP_LEVEL ? '' : moveToId,
          });
          queryClient.invalidateQueries({ queryKey: ['teams'] });
          queryClient.invalidateQueries({ queryKey: ['managed-teams'] });
          setMovingTeam(null);
          setMoveToId(defaultParentChoice);
          toast.success('Team moved.');
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="teamParent">Sits under</Label>
          <Select value={moveToId} onValueChange={setMoveToId}>
            <SelectTrigger id="teamParent"><SelectValue /></SelectTrigger>
            <SelectContent>
              {/* Only an administrator can lift a team out of every subtree. */}
              {isAdmin && <SelectItem value={TOP_LEVEL}>Nothing — top level</SelectItem>}
              {eligibleParents(teams, movingTeam?.id, canParent).map((p: any) => (
                <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            A team cannot be moved underneath itself or one of the teams below it.
            {!isAdmin && ' You can only move it to another team you run.'}
          </p>
        </div>
      </FormDialog>

      <TeamMembersDialog
        team={viewing}
        canRemove={isAdmin || (viewing ? managedTeamIds.has(viewing.id) : false)}
        onClose={() => setViewing(null)}
      />

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
            setNewParentId(defaultParentChoice);
          }
          setCreateOpen(o);
        }}
        title="New team"
        description="Teams nest: put one under another and whoever runs the parent runs it too."
        submitLabel="Create team"
        disabledReason={
          !name.trim()
            ? 'Give the team a name'
            : // A lead's team has to sit under one of theirs; the top level is
              // not one of the options they are offered.
              !newParentId
              ? 'Choose which team it sits under'
              : undefined
        }
        onSubmit={async () => {
          await orgApi.createTeam({
            name: name.trim(),
            description: description.trim() || undefined,
            parentId: newParentId === TOP_LEVEL ? undefined : newParentId,
          });
          queryClient.invalidateQueries({ queryKey: ['teams'] });
          queryClient.invalidateQueries({ queryKey: ['managed-teams'] });
          setName('');
          setDescription('');
          setNewParentId(defaultParentChoice);
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
        <div className="space-y-2">
          <Label htmlFor="newTeamParent">{isAdmin ? 'Sits under (optional)' : 'Sits under'}</Label>
          <Select value={newParentId} onValueChange={setNewParentId}>
            <SelectTrigger id="newTeamParent">
              <SelectValue placeholder="Choose a team you run" />
            </SelectTrigger>
            <SelectContent>
              {/* Only an administrator starts a new branch of the organization. */}
              {isAdmin && <SelectItem value={TOP_LEVEL}>Nothing — top level</SelectItem>}
              {eligibleParents(teams, undefined, canParent).map((p: any) => (
                <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          {!isAdmin && (
            <p className="text-xs text-muted-foreground">
              You run the new team, through the one it sits under.
            </p>
          )}
        </div>
      </FormDialog>

      <FormDialog
        open={!!addTo}
        onOpenChange={(o) => {
          if (!o) {
            setAddTo(null);
            setMemberIds([]);
          }
        }}
        title={addTo ? `Add people to ${addTo.name}` : 'Add members'}
        submitLabel={addLabel(memberIds.length, 'Add member', (n) => `Add ${n} members`)}
        disabledReason={memberIds.length === 0 ? 'Pick a member' : undefined}
        onSubmit={async () => {
          const team = addTo;
          try {
            await addEach(
              memberIds,
              (id) => orgApi.addTeamMember(team.id, id),
              (id) => users.find((u: any) => u.id === id)?.name ?? 'somebody',
              setMemberIds
            );
            setAddTo(null);
            setMemberIds([]);
          } finally {
            queryClient.invalidateQueries({ queryKey: ['teams'] });
            queryClient.invalidateQueries({ queryKey: ['team-members', team.id] });
          }
        }}
      >
        <PeoplePicker
          people={joinable.map((u: any) => ({ id: u.id, name: u.name, email: u.email, avatarUrl: u.avatarUrl }))}
          selected={memberIds}
          onChange={setMemberIds}
          empty="Everybody is already on this team."
        />
      </FormDialog>
    </>
  );
}

/** Who is on a team. A manager needs to see their own people, not just a count. */
function TeamMembersDialog({
  team,
  canRemove,
  onClose,
}: {
  team: any | null;
  canRemove: boolean;
  onClose: () => void;
}) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { statusOf, personOf } = usePresence();

  const { data, isLoading } = useQuery({
    queryKey: ['team-members', team?.id],
    queryFn: () => orgApi.listTeamMembers(team.id),
    enabled: !!team,
  });
  const members = data?.data ?? [];

  const remove = async (userId: string) => {
    try {
      await orgApi.removeTeamMember(team.id, userId);
      queryClient.invalidateQueries({ queryKey: ['team-members', team.id] });
      queryClient.invalidateQueries({ queryKey: ['teams'] });
      toast.success('Removed from the team.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not remove them.'));
    }
  };

  return (
    <Dialog open={!!team} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{team?.name ?? 'Team'}</DialogTitle>
          <DialogDescription>
            Everybody on this team, and whether they are around.
          </DialogDescription>
        </DialogHeader>

        {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
        {!isLoading && members.length === 0 && (
          <p className="text-sm text-muted-foreground">Nobody is on this team yet.</p>
        )}
        <ul className="max-h-72 space-y-1 overflow-y-auto">
          {members.map((m: any) => (
            <li key={m.id} className="flex items-center gap-3 rounded-md p-2 hover:bg-accent">
              <div className="relative shrink-0">
                <UserAvatar name={m.name} src={m.avatarUrl} fallbackClassName="bg-primary/10 font-medium text-primary" />
                <PresenceDot
                  status={statusOf(m.id)}
                  note={personOf(m.id)?.note}
                  className="absolute -bottom-0.5 -right-0.5"
                />
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{m.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  {personOf(m.id)?.note || m.email}
                </p>
              </div>
              {canRemove && (
                <button
                  onClick={() => remove(m.id)}
                  aria-label={`Remove ${m.name} from the team`}
                  className="shrink-0 text-muted-foreground hover:text-foreground"
                >
                  ×
                </button>
              )}
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}

/** A role in a list to choose from: its name, and what it lets somebody do. */
function RoleOption({ role }: { role: any }) {
  const catalog = usePermissionCatalog();
  return (
    <span className="flex min-w-0 flex-col py-0.5 text-left">
      <span>{role.name}</span>
      <span className="max-w-[18rem] truncate text-xs text-muted-foreground">{roleSummary(role, catalog)}</span>
    </span>
  );
}
