import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { orgApi } from '@/shared/api/client';
import { useAuthStore } from '@/shared/store/authStore';
import { Avatar, AvatarFallback, AvatarImage } from '@/shared/components/ui/avatar';
import { cn } from '@/shared/utils/cn';

/** Up to two initials: "Ada Lovelace" is AL, "ada" is A. */
export function initialsOf(name: string | undefined): string {
  const words = (name ?? '').trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return '?';
  const first = words[0][0] ?? '';
  const last = words.length > 1 ? words[words.length - 1][0] ?? '' : '';
  return (first + last).toUpperCase();
}

/** Somebody's picture, or their initials until it loads or when they have none. */
export function UserAvatar({
  name,
  src,
  className,
  fallbackClassName,
}: {
  name?: string;
  src?: string | null;
  className?: string;
  fallbackClassName?: string;
}) {
  return (
    <Avatar className={cn('h-8 w-8', className)}>
      {src && <AvatarImage src={src} alt={name ? `${name}'s picture` : ''} className="object-cover" />}
      <AvatarFallback className={cn('text-xs', fallbackClassName)}>{initialsOf(name)}</AvatarFallback>
    </Avatar>
  );
}

/**
 * Who has which picture, from the organization's people: what most of the app
 * has for a person is their id and name, not their picture. The same query the
 * rest of the app reads, so it costs nothing extra.
 */
export function usePictures(): (userId: string | null | undefined) => string | undefined {
  // A guest in a meeting has no account to ask with, and is shown initials.
  const signedIn = useAuthStore((s) => s.isAuthenticated);
  const { data } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: signedIn });
  const byId = useMemo(() => {
    const map = new Map<string, string>();
    for (const u of (data?.data ?? []) as Array<{ id: string; avatarUrl?: string | null }>) {
      if (u.avatarUrl) map.set(u.id, u.avatarUrl);
    }
    return map;
  }, [data]);
  return (userId) => (userId ? byId.get(userId) : undefined);
}

/** Somebody's picture, found by who they are, or their initials. */
export function PersonAvatar({
  userId,
  name,
  src,
  className,
  fallbackClassName,
}: {
  userId?: string | null;
  name?: string;
  /** A picture already to hand, used before looking one up. */
  src?: string | null;
  className?: string;
  fallbackClassName?: string;
}) {
  const pictureOf = usePictures();
  return <UserAvatar name={name} src={src || pictureOf(userId)} className={className} fallbackClassName={fallbackClassName} />;
}
