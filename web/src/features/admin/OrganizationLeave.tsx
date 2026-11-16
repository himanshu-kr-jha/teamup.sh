import { Holidays, LeaveBoard } from '@/features/leave/LeavePage';
import { localToday } from '@/shared/api/client';

/**
 * The organization's holidays, and everybody's leave, for whoever runs the
 * organization. Each person's own leave is under Leave, from their profile menu.
 */
export function OrganizationLeave({ keepsHolidays, readsEverybody }: { keepsHolidays: boolean; readsEverybody: boolean }) {
  const today = localToday();
  return (
    <div className="mx-auto max-w-3xl space-y-8">
      <Holidays today={today} keeps={keepsHolidays} />
      {readsEverybody && (
        <section aria-label="Everybody's leave" className="space-y-3">
          <div>
            <h2 className="text-sm font-semibold">Everybody&rsquo;s leave</h2>
            <p className="text-sm text-muted-foreground">
              Add or correct leave for anybody. They are told when somebody else changes theirs, and every change is kept.
            </p>
          </div>
          <LeaveBoard />
        </section>
      )}
    </div>
  );
}
