/**
 * What the server says this caller may do to a task.
 *
 * A manager's team board shows the work of everybody on their team, including
 * an organisation admin who happens to sit on it as an ordinary engineer. That
 * is deliberate: hiding somebody's work hides the work. What is not allowed is
 * the board offering an action the API will refuse, so every task comes back
 * carrying this, worked out from the same rule the write paths enforce.
 */
export interface TaskAbilities {
  /** Title, description, status, priority, dates. */
  edit: boolean;
  /** Move the work off whoever is holding it. */
  reassign: boolean;
  delete: boolean;
  /** Why, in words a person can read: "Quinn Doe is an organisation admin". */
  reason?: string;
}

/**
 * The sentence to show on a task this caller cannot fully act on, or null when
 * there is nothing to say. A greyed card with no explanation is nearly as bad
 * as the 403 it is standing in for, so the consequence and the cause are said
 * together.
 */
export function restrictionNote(can?: TaskAbilities): string | null {
  if (!can) return null;
  const barred: string[] = [];
  if (!can.reassign) barred.push('reassign');
  if (!can.delete) barred.push('delete');
  if (barred.length === 0) return null;

  const what = `You can see this task but cannot ${barred.join(' or ')} it`;
  return can.reason ? `${what} - ${can.reason}.` : `${what}.`;
}
