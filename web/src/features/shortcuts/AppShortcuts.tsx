import { useNavigate } from 'react-router-dom';
import { overlayOpen, useShortcut, useShortcutPanels } from '@/shared/shortcuts/ShortcutsProvider';
import { CheatSheet } from './CheatSheet';
import { CommandPalette } from './CommandPalette';
import { usePlaces } from './places';

/**
 * The shortcuts that work on every page: the palette, the cheat sheet and
 * going places. A page's own (C for a new task, Alt+arrows in chat) live with
 * that page.
 */
export function AppShortcuts() {
  const navigate = useNavigate();
  const places = usePlaces();
  const { help, palette, setHelp, setPalette } = useShortcutPanels();

  useShortcut('palette', () => setPalette(!palette), { inDialogs: true });
  // "?" closes the sheet it opened, but does not open it over somebody
  // else's dialog, where it may well be a question mark being typed.
  useShortcut(
    'help',
    () => {
      if (help) setHelp(false);
      else if (overlayOpen()) return false;
      else setHelp(true);
    },
    { inDialogs: true }
  );

  useShortcut('go.chat', () => navigate('/chat'));
  useShortcut('go.tasks', () => navigate('/tasks'));
  useShortcut('go.calendar', () => navigate('/calendar'));
  useShortcut('go.notes', () => navigate('/meeting-notes'), { enabled: places.notes });
  useShortcut('go.projects', () => navigate('/projects'));
  useShortcut('go.organization', () => navigate('/organization'), { enabled: places.organization });
  useShortcut('go.settings', () => navigate('/settings'));

  return (
    <>
      <CheatSheet open={help} onOpenChange={setHelp} />
      <CommandPalette open={palette} onOpenChange={setPalette} />
    </>
  );
}
