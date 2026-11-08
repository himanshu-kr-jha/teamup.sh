import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { brandingApi, type Branding } from '@/shared/api/client';
import { applyBrandColor } from '@/shared/branding';

export const BRANDING_QUERY_KEY = ['branding'] as const;

/** The organization's look. Readable before signing in. */
export function useBranding(): Branding | undefined {
  const { data } = useQuery({
    queryKey: BRANDING_QUERY_KEY,
    queryFn: async () => (await brandingApi.get()).data,
    staleTime: 5 * 60 * 1000,
    // An administrator changing it is seen by everybody else the next time
    // they come back to the tab, without a reload.
    refetchOnWindowFocus: true,
  });
  return data;
}

/** Keeps the page in the organization's colour. Mounted once, above the router. */
export function BrandingSync() {
  const branding = useBranding();
  useEffect(() => {
    if (branding) applyBrandColor(branding.color);
  }, [branding]);
  return null;
}
