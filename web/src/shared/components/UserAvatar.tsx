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
