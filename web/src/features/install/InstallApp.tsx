import { useState, useSyncExternalStore } from 'react';
import { useLocation } from 'react-router-dom';
import { CheckCircle2, Download, Loader2, Share, PlusSquare, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { useAuthStore } from '@/shared/store/authStore';
import { useActiveCall } from '@/features/calls/ActiveCallProvider';
import { isUpdateReady, subscribeToUpdate } from '@/sw/register';
import { dismissInstall, install, installDismissed, useInstallWay } from './install';

/** How it is done on an iPhone, where Safari has no install button for a page to press. */
function IOSSteps() {
  return (
    <ol className="space-y-3 text-sm">
      <li className="flex items-start gap-3">
        <Step n={1} />
        <span className="pt-0.5">
          Tap <Share className="mx-0.5 inline h-4 w-4 -translate-y-px text-primary" aria-label="Share" /> <strong>Share</strong> in Safari&apos;s bar.
        </span>
      </li>
      <li className="flex items-start gap-3">
        <Step n={2} />
        <span className="pt-0.5">
          Scroll down and tap <PlusSquare className="mx-0.5 inline h-4 w-4 -translate-y-px" aria-hidden="true" /> <strong>Add to Home Screen</strong>, then <strong>Add</strong>.
        </span>
      </li>
      <li className="flex items-start gap-3">
        <Step n={3} />
        <span className="pt-0.5">
          Open TeamUp from your home screen, sign in, and turn on notifications in Settings. On an iPhone they only work there.
        </span>
      </li>
    </ol>
  );
}

function Step({ n }: { n: number }) {
  return (
    <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary" aria-hidden="true">
      {n}
    </span>
  );
}

function IOSDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[calc(100%-2rem)] max-w-sm rounded-xl">
        <DialogHeader>
          <DialogTitle className="pr-6">Add TeamUp to your home screen</DialogTitle>
          <DialogDescription>It opens like any other app, full screen, and can notify you of calls and messages.</DialogDescription>
        </DialogHeader>
        <IOSSteps />
      </DialogContent>
    </Dialog>
  );
}

/** Asks the browser to install, with a spinner while its own dialog is up. */
function useInstallButton() {
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      await install();
    } finally {
      setBusy(false);
    }
  };
  return { busy, run };
}

/** Settings' card: install from here on any device that allows it, or how to. */
export function InstallCard() {
  const way = useInstallWay();
  const { busy, run } = useInstallButton();
  return (
    <Card data-testid="install-card">
      <CardHeader>
        <CardTitle>TeamUp app</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {way === 'installed' ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <CheckCircle2 className="h-4 w-4 text-green-600 dark:text-green-500" aria-hidden="true" />
            TeamUp is installed on this device.
          </p>
        ) : way === 'prompt' ? (
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-sm text-muted-foreground">
              Install TeamUp on this device: it opens in a window of its own, from your home screen or app list, like any other app.
            </p>
            <Button onClick={() => void run()} disabled={busy} className="shrink-0">
              {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" /> : <Download className="mr-2 h-4 w-4" aria-hidden="true" />}
              Install TeamUp
            </Button>
          </div>
        ) : way === 'ios' ? (
          <>
            <p className="text-sm text-muted-foreground">Add TeamUp to your home screen to use it like an app, and to get notifications.</p>
            <IOSSteps />
          </>
        ) : (
          <p className="text-sm text-muted-foreground">
            On a phone, open TeamUp in Chrome on Android or Safari on an iPhone and add it to your home screen. On a computer, Chrome and Edge
            install it from the icon at the end of the address bar.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

/**
 * A suggestion on a phone to put TeamUp on the home screen, once the welcome
 * tour is over. Put away, it stays away on this device; Settings still has it.
 * Not over a call, and not alongside the offer of a new version.
 */
export function InstallBanner() {
  const way = useInstallWay();
  const user = useAuthStore((s) => s.user);
  const { activeCall, joining } = useActiveCall();
  const update = useSyncExternalStore(subscribeToUpdate, isUpdateReady);
  const { pathname } = useLocation();
  const [hidden, setHidden] = useState(installDismissed);
  const [steps, setSteps] = useState(false);
  const { busy, run } = useInstallButton();

  const phone = typeof window !== 'undefined' && window.matchMedia('(max-width: 1023px) and (pointer: coarse)').matches;
  const show =
    phone && !hidden && (way === 'prompt' || way === 'ios') && !!user?.tourCompletedAt && !activeCall && !joining && !update && !pathname.startsWith('/calls/');

  const putAway = () => {
    dismissInstall();
    setHidden(true);
  };

  return (
    <>
      {show && (
        // Centred by the wrapper, not by a transform: the entrance animation
        // replaces an element's transform while it runs, so a banner centred by
        // one came in from the right of the screen.
        <div className="pointer-events-none fixed inset-x-4 bottom-[calc(var(--toast-bottom)+1rem)] z-40 flex justify-center">
          <div
            role="region"
            aria-label="Install TeamUp"
            data-testid="install-banner"
            className="pointer-events-auto flex w-full max-w-md animate-in fade-in slide-in-from-bottom-2 items-center gap-3 rounded-xl border bg-card py-2.5 pl-3 pr-2 text-card-foreground shadow-lg"
          >
            <img src="/icons/icon-192.png" alt="" className="h-9 w-9 shrink-0 rounded-lg" />
            <div className="min-w-0 flex-1 leading-tight">
              <p className="text-sm font-medium">Get the TeamUp app</p>
              <p className="truncate text-xs text-muted-foreground">On your home screen</p>
            </div>
            <Button size="sm" onClick={() => (way === 'ios' ? setSteps(true) : void run())} disabled={busy}>
              {busy && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" aria-hidden="true" />}
              Install
            </Button>
            <Button size="icon" variant="ghost" className="h-8 w-8 shrink-0" aria-label="Not now" onClick={putAway}>
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
      <IOSDialog
        open={steps}
        onOpenChange={(open) => {
          setSteps(open);
          // Shown how, the suggestion has done its job.
          if (!open) putAway();
        }}
      />
    </>
  );
}
