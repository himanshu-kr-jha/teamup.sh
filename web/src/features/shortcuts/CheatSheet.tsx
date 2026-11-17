import { Fragment } from 'react';
import { Link } from 'react-router-dom';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { IS_MAC, comboKeys, parseCombo } from '@/shared/shortcuts/keys';
import { MOD_NAME, SECTIONS, SHORTCUTS, type ShortcutInfo, type ShortcutSection } from '@/shared/shortcuts/catalogue';
import { useSingleKeys } from '@/shared/shortcuts/preference';
import { usePlaces } from './places';

/** A key as it is printed on the keyboard. */
export function Kbd({ children }: { children: string }) {
  return (
    <kbd className="inline-flex h-6 min-w-[1.5rem] items-center justify-center rounded border border-border bg-muted px-1.5 font-sans text-xs font-medium text-foreground shadow-[inset_0_-1px_0_hsl(var(--border))]">
      {children}
    </kbd>
  );
}

/** One way of pressing a shortcut: Ctrl + D, or G then T. */
function Keys({ text }: { text: string }) {
  const steps = comboKeys(parseCombo(text), IS_MAC);
  return (
    <span className="inline-flex items-center gap-1">
      {steps.map((step, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="px-0.5 text-xs text-muted-foreground">then</span>}
          {step.map((k) => (
            <Kbd key={k}>{k}</Kbd>
          ))}
        </Fragment>
      ))}
    </span>
  );
}

const COLUMNS: ShortcutSection[][] = [
  ['Anywhere', 'Go to'],
  ['In a call', 'Tasks', 'Chat'],
];

function Row({ shortcut }: { shortcut: ShortcutInfo }) {
  return (
    <li className="flex items-center justify-between gap-3 py-1">
      <span className="min-w-0 text-sm">{shortcut.label}</span>
      <span className="flex shrink-0 flex-wrap items-center justify-end gap-x-2 gap-y-1">
        {shortcut.keys.map((k, i) => (
          <Fragment key={k}>
            {i > 0 && <span className="text-xs text-muted-foreground">or</span>}
            <Keys text={k} />
          </Fragment>
        ))}
      </span>
    </li>
  );
}

/** Every shortcut, by where it works. Opened with ? from anywhere. */
export function CheatSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const places = usePlaces();
  const singleKeys = useSingleKeys();
  const shown = SHORTCUTS.filter(
    (s) => (s.id !== 'go.notes' || places.notes) && (s.id !== 'go.organization' || places.organization)
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl gap-4 p-5 sm:p-6" data-testid="shortcut-sheet">
        <DialogHeader className="pr-6">
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>
            {singleKeys
              ? 'Single keys work when you are not typing in a box.'
              : `Single-key shortcuts are off, so only the ${MOD_NAME} ones work.`}{' '}
            <Link to="/settings?tab=appearance" className="text-primary underline-offset-4 hover:underline" onClick={() => onOpenChange(false)}>
              {singleKeys ? 'Turn single keys off' : 'Turn them on'}
            </Link>
          </DialogDescription>
        </DialogHeader>

        {/* Two columns of about the same height on a wide screen; one after
            the other, in the same order, on a narrow one. */}
        <div className="grid gap-x-8 gap-y-4 md:grid-cols-2">
          {COLUMNS.map((column, i) => (
            <div key={i} className="min-w-0 space-y-4">
              {column.map((name) => {
                const section = SECTIONS.find((s) => s.name === name)!;
                const rows = shown.filter((s) => s.section === name);
                const headingId = `shortcuts-${name.replace(/\s+/g, '-').toLowerCase()}`;
                return (
                  <section key={name} aria-labelledby={headingId} className="min-w-0">
                    <h3 id={headingId} className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      {name}
                    </h3>
                    {section.note && <p className="mt-0.5 text-xs text-muted-foreground">{section.note}</p>}
                    <ul className="mt-1 divide-y divide-border/60">
                      {rows.map((s) => (
                        <Row key={s.id} shortcut={s} />
                      ))}
                    </ul>
                  </section>
                );
              })}
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
