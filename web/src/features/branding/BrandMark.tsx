import { Hash } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { useBranding } from './useBranding';

const LOGO = { sm: 'h-6 max-w-[6rem]', md: 'h-8 max-w-[8rem]', lg: 'h-10 max-w-[10rem]' };
const ICON = { sm: 'h-6 w-6', md: 'h-8 w-8', lg: 'h-10 w-10' };
const TEXT = { sm: 'text-lg', md: 'text-xl', lg: 'text-2xl' };

/**
 * The organization's logo and name, or TeamUp's own mark when no logo has been
 * set. The name is the organization's only once it has a logo: a stock icon
 * beside a company's name looks like a placeholder.
 */
export function BrandMark({
  size = 'md',
  showName = true,
  className,
}: {
  size?: 'sm' | 'md' | 'lg';
  showName?: boolean;
  className?: string;
}) {
  const branding = useBranding();
  const logo = branding?.logoUrl;
  const name = logo ? branding?.name || 'TeamUp' : 'TeamUp';
  return (
    <span className={cn('flex min-w-0 items-center gap-2', className)}>
      {logo ? (
        <img
          src={logo}
          alt=""
          className={cn('shrink-0 rounded object-contain', LOGO[size])}
          data-testid="brand-logo"
        />
      ) : (
        <Hash className={cn('shrink-0 text-primary', ICON[size])} aria-hidden="true" />
      )}
      {showName && (
        <span className={cn('truncate font-bold', TEXT[size])}>{name}</span>
      )}
    </span>
  );
}
