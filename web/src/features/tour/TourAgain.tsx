import { useState } from 'react';
import { Compass, Loader2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { tourApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuthStore } from '@/shared/store/authStore';

/** Settings' way back to the welcome tour, for whoever skipped it or wants a reminder. */
export function TourAgain() {
  const toast = useToast();
  const setUser = useAuthStore((s) => s.setUser);
  const [busy, setBusy] = useState(false);

  const again = async () => {
    setBusy(true);
    try {
      const { data } = await tourApi.set(false);
      const user = useAuthStore.getState().user;
      if (user) setUser({ ...user, tourCompletedAt: data.tourCompletedAt });
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not start the tour.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Getting started</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm text-muted-foreground">A quick look around TeamUp, as you saw it the first time you signed in.</p>
        <Button variant="outline" onClick={() => void again()} disabled={busy} className="shrink-0">
          {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <Compass className="mr-2 h-4 w-4" aria-hidden="true" />}
          Show the tour again
        </Button>
      </CardContent>
    </Card>
  );
}
