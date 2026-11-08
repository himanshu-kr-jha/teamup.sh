import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Pencil, Plus, Shield, Trash2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Badge } from '@/shared/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { FormDialog } from '@/shared/components/FormDialog';
import { orgApi, type PermissionInfo, type Role } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';
import { EVERY, grants } from '@/shared/permissions';
import { cn } from '@/shared/utils/cn';

/** Every permission, as the server lists them; the same query everywhere. */
export function usePermissionCatalog(): PermissionInfo[] {
  const { data } = useQuery({
    queryKey: ['permissions'],
    queryFn: async () => (await orgApi.listPermissions()).data.permissions,
    staleTime: Infinity,
  });
  return data ?? [];
}

/** What a role lets somebody do, in a few words. */
export function roleSummary(role: Pick<Role, 'permissions' | 'name'>, catalog: PermissionInfo[]): string {
  const perms = role.permissions ?? [];
  if (perms.includes(EVERY)) return 'Everything';
  if (perms.length === 0) return 'Their own work only';
  if (catalog.length > 0 && perms.length === catalog.length) return 'Everything except what only an owner can do';
  return perms.map((p) => catalog.find((c) => c.key === p)?.label ?? p).join(', ');
}

/** Whether somebody holding `held` may hand this role out: it can do no more than they can. */
export function withinReach(role: Pick<Role, 'permissions'>, held: string[] | undefined): boolean {
  return (role.permissions ?? []).every((p) => grants(held, p));
}

function byArea(catalog: PermissionInfo[]): Array<{ area: string; items: PermissionInfo[] }> {
  const out: Array<{ area: string; items: PermissionInfo[] }> = [];
  for (const p of catalog) {
    const group = out.find((g) => g.area === p.area);
    if (group) group.items.push(p);
    else out.push({ area: p.area, items: [p] });
  }
  return out;
}

/**
 * The organization's roles, and what each lets somebody do. The built-in ones
 * stay as they are; any other is made here from a checklist of permissions,
 * none of which can be one the person making it does not hold.
 */
export function RolesTab({ canManage }: { canManage: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const catalog = usePermissionCatalog();
  const [editing, setEditing] = useState<Role | 'new' | null>(null);
  const { data, isLoading } = useQuery({ queryKey: ['roles'], queryFn: () => orgApi.listRoles() });
  const roles = (data?.data ?? []) as Role[];

  const remove = async (role: Role) => {
    try {
      await orgApi.deleteRole(role.id);
      queryClient.invalidateQueries({ queryKey: ['roles'] });
      toast.success(`The ${role.name} role was deleted.`);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not delete that role.'));
    }
  };

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading roles…</p>;

  return (
    <>
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0">
          <div>
            <CardTitle>Roles</CardTitle>
            <p className="mt-1 text-sm text-muted-foreground">
              What somebody may do beyond their own work. Everybody can chat, join calls they are invited to and run their own tasks.
            </p>
          </div>
          {canManage && (
            <Button size="sm" onClick={() => setEditing('new')}>
              <Plus className="mr-1 h-4 w-4" />
              New role
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          {roles.map((r) => {
            const mine = withinReach(r, user?.permissions);
            return (
              <div key={r.id} className="flex items-start gap-3 rounded-lg border border-border p-3" data-testid="role">
                <Shield className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-2 font-medium">
                    {r.name}
                    {r.isSystem && <Badge variant="secondary">built in</Badge>}
                  </p>
                  {r.description && <p className="text-sm">{r.description}</p>}
                  <p className="text-sm text-muted-foreground">{roleSummary(r, catalog)}</p>
                </div>
                {canManage && !r.isSystem && mine && (
                  <div className="flex shrink-0 gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setEditing(r)} aria-label={`Change the ${r.name} role`}>
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => remove(r)} aria-label={`Delete the ${r.name} role`}>
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </div>
            );
          })}
        </CardContent>
      </Card>

      <RoleEditor
        role={editing}
        roles={roles}
        catalog={catalog}
        held={user?.permissions}
        onOpenChange={(open) => !open && setEditing(null)}
        onSaved={() => queryClient.invalidateQueries({ queryKey: ['roles'] })}
      />
    </>
  );
}

function RoleEditor({
  role,
  roles,
  catalog,
  held,
  onOpenChange,
  onSaved,
}: {
  role: Role | 'new' | null;
  roles: Role[];
  catalog: PermissionInfo[];
  held: string[] | undefined;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const existing = role && role !== 'new' ? role : null;
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [chosen, setChosen] = useState<string[]>([]);
  const [startFrom, setStartFrom] = useState('');
  const [openedFor, setOpenedFor] = useState<Role | 'new' | null>(null);

  // Filled in each time it opens, for a new role or the one being changed.
  if (role !== openedFor) {
    setOpenedFor(role);
    setName(existing?.name ?? '');
    setDescription(existing?.description ?? '');
    setChosen(existing?.permissions.filter((p) => p !== EVERY) ?? []);
    setStartFrom('');
  }

  const allowed = (key: string) => grants(held, key);
  const groups = useMemo(() => byArea(catalog), [catalog]);
  const toggle = (key: string, on: boolean) =>
    setChosen((current) => (on ? [...new Set([...current, key])] : current.filter((k) => k !== key)));

  // Any role can be the starting point; what this person cannot grant is left out.
  const presets = roles.filter((r) => r.id !== existing?.id);
  const applyPreset = (id: string) => {
    setStartFrom(id);
    const from = presets.find((r) => r.id === id);
    if (!from) return;
    const keys = from.permissions.includes(EVERY) ? catalog.map((c) => c.key) : from.permissions;
    setChosen(keys.filter(allowed));
  };

  const nameTaken = roles.some((r) => r.id !== existing?.id && r.name.trim().toLowerCase() === name.trim().toLowerCase());

  return (
    <FormDialog
      open={!!role}
      onOpenChange={onOpenChange}
      title={existing ? `Change the ${existing.name} role` : 'New role'}
      description="Tick what people with this role may do. You can only give permissions you have yourself."
      submitLabel={existing ? 'Save role' : 'Create role'}
      disabledReason={!name.trim() ? 'Give the role a name.' : nameTaken ? 'There is already a role with that name.' : undefined}
      onSubmit={async () => {
        const body = { name: name.trim(), description: description.trim(), permissions: chosen };
        if (existing) await orgApi.updateRole(existing.id, body);
        else await orgApi.createRole(body);
        onSaved();
        toast.success(existing ? `The ${body.name} role was saved.` : `The ${body.name} role was created. Give it to people under Members.`);
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="roleName">Name</Label>
        <Input id="roleName" value={name} onChange={(e) => setName(e.target.value)} placeholder="Office manager" maxLength={60} />
      </div>
      <div className="space-y-2">
        <Label htmlFor="roleDescription">What it is for</Label>
        <Input
          id="roleDescription"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Runs the office: members, settings and the calendar"
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="roleStartFrom">Start from</Label>
        <Select value={startFrom} onValueChange={applyPreset}>
          <SelectTrigger id="roleStartFrom">
            <SelectValue placeholder="Another role's permissions" />
          </SelectTrigger>
          <SelectContent>
            {presets.map((r) => (
              <SelectItem key={r.id} value={r.id}>
                {r.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <fieldset className="space-y-4">
        <legend className="flex w-full items-center justify-between text-sm font-medium">
          <span>Permissions</span>
          <span className="text-xs font-normal text-muted-foreground" data-testid="permission-count">
            {chosen.length} of {catalog.length}
          </span>
        </legend>
        {groups.map((g) => {
          const reachable = g.items.filter((p) => allowed(p.key));
          const allOn = reachable.length > 0 && reachable.every((p) => chosen.includes(p.key));
          return (
            <div key={g.area} className="space-y-1.5" role="group" aria-label={g.area}>
              <div className="flex items-center justify-between">
                <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{g.area}</p>
                {reachable.length > 0 && (
                  <button
                    type="button"
                    className="text-xs text-primary hover:underline"
                    onClick={() => reachable.forEach((p) => toggle(p.key, !allOn))}
                  >
                    {allOn ? 'None' : 'All'}
                  </button>
                )}
              </div>
              <ul className="divide-y divide-border rounded-md border border-border">
                {g.items.map((p) => {
                  const ok = allowed(p.key);
                  const on = chosen.includes(p.key);
                  return (
                    <li key={p.key}>
                      <label
                        className={cn('flex gap-3 p-2.5', ok ? 'cursor-pointer hover:bg-accent/50' : 'cursor-not-allowed opacity-60')}
                        title={ok ? undefined : 'You do not have this permission, so you cannot give it.'}
                      >
                        <span className="relative mt-0.5 flex h-4 w-4 shrink-0">
                          <input
                            type="checkbox"
                            className="peer h-4 w-4 cursor-[inherit] appearance-none rounded border border-input bg-background checked:border-primary checked:bg-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            checked={on}
                            disabled={!ok}
                            onChange={(e) => toggle(p.key, e.target.checked)}
                            aria-describedby={`perm-${p.key}`}
                          />
                          <Check className="pointer-events-none absolute inset-0 hidden h-4 w-4 p-0.5 text-primary-foreground peer-checked:block" aria-hidden="true" />
                        </span>
                        <span className="min-w-0">
                          <span className="block text-sm font-medium">{p.label}</span>
                          <span id={`perm-${p.key}`} className="block text-xs text-muted-foreground">
                            {p.description}
                            {!ok && ' You do not have this, so you cannot give it.'}
                          </span>
                        </span>
                      </label>
                    </li>
                  );
                })}
              </ul>
            </div>
          );
        })}
      </fieldset>
    </FormDialog>
  );
}
