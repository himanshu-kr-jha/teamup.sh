import { useAuth } from '@/features/auth/AuthProvider';
import { PERMS, can } from '@/shared/permissions';
import { ProjectsTab } from './ProjectsTab';

/**
 * The organization's projects, for everybody: what each is made of, and your
 * access to its code. Administrators manage them here too.
 */
export function ProjectsPage() {
  const { user } = useAuth();
  return (
    <div className="flex h-full flex-col bg-background">
      <div className="flex h-16 shrink-0 items-center border-b border-border px-4 md:px-6">
        <h1 className="text-2xl font-bold">Projects</h1>
      </div>
      <div className="flex-1 overflow-y-auto p-4 md:p-6">
        <ProjectsTab canManage={can(user, PERMS.orgSettings)} userId={user?.id} />
      </div>
    </div>
  );
}
