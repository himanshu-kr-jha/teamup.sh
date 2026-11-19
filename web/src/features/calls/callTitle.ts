/**
 * What a call is called at the top of its window, for whoever is looking.
 *
 * A call made from a conversation between two people is named, when it is made,
 * after the conversation as the caller sees it: the other person. The person
 * called would then see their own name over the call, so for them it is the
 * caller's name instead.
 */
export function callTitleFor(
  title: string | undefined,
  participants: { userId: string; userName?: string }[] | undefined,
  myId: string | undefined
): string | undefined {
  const named = title?.trim() || undefined;
  if (!named || !myId || participants?.length !== 2) return named;
  const me = participants.find((p) => p.userId === myId);
  const other = participants.find((p) => p.userId !== myId);
  if (me?.userName?.trim() === named && other?.userName?.trim()) return other.userName.trim();
  return named;
}

/** "Alice" for "Alice Archer": what fits on a small tile. */
export function firstNameOf(name: string): string {
  return name.trim().split(/\s+/)[0] || name;
}
