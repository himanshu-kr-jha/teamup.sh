import { describe, expect, it, vi } from 'vitest';
import { restoreDraft, restoreStaged, whenVisible } from './composerDraft';

describe('restoreDraft', () => {
  it('puts back what failed to send', () => {
    expect(restoreDraft('', 'a long update')).toBe('a long update');
  });

  it('keeps anything typed since, as well as what failed', () => {
    expect(restoreDraft('and another', 'a long update')).toBe('a long update\nand another');
  });

  it('does not add a blank line for a file sent with no words', () => {
    expect(restoreDraft('typing', '')).toBe('typing');
  });
});

describe('restoreStaged', () => {
  const file = (id: string) => ({ id, filename: id }) as never;

  it('offers the attachments again, ahead of anything staged since', () => {
    expect(restoreStaged([file('new')], [file('old')]).map((f: { id: string }) => f.id)).toEqual(['old', 'new']);
  });

  it('never offers one twice', () => {
    expect(restoreStaged([file('a')], [file('a')])).toHaveLength(1);
  });
});

describe('whenVisible', () => {
  function fakeDocument(visibilityState: string) {
    const listeners = new Set<() => void>();
    return {
      visibilityState,
      addEventListener: (_: string, fn: () => void) => listeners.add(fn),
      removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
      fire: () => listeners.forEach((fn) => fn()),
    };
  }

  it('runs straight away in a visible tab', () => {
    const run = vi.fn();
    whenVisible(fakeDocument('visible'), run);
    expect(run).toHaveBeenCalledOnce();
  });

  it('waits in a hidden tab until it is looked at', () => {
    const doc = fakeDocument('hidden');
    const run = vi.fn();
    whenVisible(doc, run);
    expect(run).not.toHaveBeenCalled();

    doc.fire();
    expect(run).not.toHaveBeenCalled();

    doc.visibilityState = 'visible';
    doc.fire();
    expect(run).toHaveBeenCalledOnce();
  });

  it('stops waiting once cancelled', () => {
    const doc = fakeDocument('hidden');
    const run = vi.fn();
    const cancel = whenVisible(doc, run);
    cancel();
    doc.visibilityState = 'visible';
    doc.fire();
    expect(run).not.toHaveBeenCalled();
  });
});
