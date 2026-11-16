import { useRef } from 'react';
import { Palmtree } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import type { Person } from './usePresence';
import { backOnText, leaveSentence } from './leaveNotice';

/**
 * Above the message box of a conversation with somebody on leave, so nobody
 * expects an answer before they are back.
 */
export function OnLeaveBanner({ person }: { person: Person }) {
  return (
    <div
      role="status"
      className="mb-2 flex items-start gap-2 rounded-md border border-slate-300 bg-slate-100 px-3 py-2 text-sm text-slate-700 dark:border-slate-600 dark:bg-slate-800/60 dark:text-slate-200"
      data-testid="on-leave-banner"
    >
      <Palmtree className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <span>
        {leaveSentence(person)} Your message will wait for them; please do not chase them while they are away.
      </span>
    </div>
  );
}

/**
 * Pressing Call for somebody on leave: their phone would not ring anyway, so say
 * so first, and offer to write instead.
 */
export function OnLeaveCallDialog({
  open,
  person,
  onWrite,
  onClose,
}: {
  open: boolean;
  person: Person;
  /** After the dialog has closed: put the cursor in the message box. */
  onWrite: () => void;
  onClose: () => void;
}) {
  const first = person.name.split(' ')[0];
  // Writing goes to the message box once the dialog has gone, rather than back
  // to the Call button that opened it.
  const writing = useRef(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) writing.current = false;
        else onClose();
      }}
    >
      <DialogContent
        className="sm:max-w-md"
        onCloseAutoFocus={(e) => {
          if (!writing.current) return;
          writing.current = false;
          e.preventDefault();
          onWrite();
        }}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Palmtree className="h-5 w-5 text-slate-500" aria-hidden="true" />
            {first} is on leave
          </DialogTitle>
          <DialogDescription>
            {first} is {backOnText(person.until)}. Calls do not ring while somebody is on leave, so this call would only
            reach them as a missed call. Write to them instead, and they will read it when they are back.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2 sm:gap-0">
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
          <Button
            onClick={() => {
              writing.current = true;
              onClose();
            }}
          >
            Write a message
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
