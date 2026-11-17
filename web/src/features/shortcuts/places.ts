import { useAuth } from '@/features/auth/AuthProvider';
import { useOrganizationAccess } from '@/features/layout/useOrganizationAccess';
import { readsNotes } from '@/shared/permissions';

/** Who may go where, so neither a key, the palette nor the cheat sheet offers a closed door. */
export function usePlaces() {
  const { user } = useAuth();
  const { allowed: mayAdminister } = useOrganizationAccess();
  return { notes: readsNotes(user), organization: mayAdminister };
}
