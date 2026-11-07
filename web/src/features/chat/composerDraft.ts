/** The composer after a send failed: nothing that was typed, then or since, is lost. */
export function restoreDraft(current: string, failed: string): string {
  if (!failed) return current;
  return current ? `${failed}\n${current}` : failed;
}

/** The attachments after a send failed. They are already uploaded, so they just go back on the tray. */
export function restoreStaged<T extends { id: string }>(current: T[], failed: T[]): T[] {
  return [...failed, ...current.filter((file) => !failed.some((f) => f.id === file.id))];
}

interface VisibilitySource {
  visibilityState: string;
  addEventListener: (type: 'visibilitychange', listener: () => void) => void;
  removeEventListener: (type: 'visibilitychange', listener: () => void) => void;
}

/**
 * Runs something now if the tab is being looked at, otherwise the moment it is.
 * Returns the way to stop waiting.
 */
export function whenVisible(source: VisibilitySource, run: () => void): () => void {
  if (source.visibilityState === 'visible') {
    run();
    return () => undefined;
  }
  const check = () => {
    if (source.visibilityState !== 'visible') return;
    source.removeEventListener('visibilitychange', check);
    run();
  };
  source.addEventListener('visibilitychange', check);
  return () => source.removeEventListener('visibilitychange', check);
}
