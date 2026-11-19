import { describe, expect, it } from 'vitest';
import { frameStream } from './frameStream';

// Frames stand in for VideoFrame: all that matters here is whether they are closed.
type Fake = { id: number; closed: boolean; close(): void };
const frame = (id: number): Fake => ({
  id,
  closed: false,
  close() {
    this.closed = true;
  },
});
const asFrame = (f: Fake) => f as unknown as VideoFrame;

/** Puts out a new frame for each one given, and closes the one given, as the processor does. */
const process = async (input: VideoFrame, c: TransformStreamDefaultController<VideoFrame>) => {
  const given = input as unknown as Fake;
  c.enqueue(asFrame(frame(given.id + 100)));
  given.close();
};

describe('frameStream', () => {
  it('passes every frame through, in order, and ends when the writing does', async () => {
    const s = frameStream(process);
    const writer = s.writable.getWriter();
    const reader = s.readable.getReader();
    const ins = [frame(1), frame(2), frame(3)];
    const seen: number[] = [];
    const reading = (async () => {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) return;
        seen.push((value as unknown as Fake).id);
        value.close();
      }
    })();
    for (const f of ins) await writer.write(asFrame(f));
    await writer.close();
    await reading;
    expect(seen).toEqual([101, 102, 103]);
    expect(ins.every((f) => f.closed)).toBe(true);
  });

  it('closes the frame waiting to be read when the reading side goes away', async () => {
    const made: Fake[] = [];
    const s = frameStream(async (input, c) => {
      const out = frame(1);
      made.push(out);
      c.enqueue(asFrame(out));
      input.close();
    });
    const writer = s.writable.getWriter();
    const write = writer.write(asFrame(frame(0)));
    // Written and processed, never read: then the reader goes.
    await new Promise((r) => setTimeout(r, 0));
    await s.readable.cancel();
    await write;
    expect(made[0].closed).toBe(true);
    // And anything written after that is closed as it comes.
    const late = frame(2);
    await writer.write(asFrame(late));
    expect(late.closed).toBe(true);
  });

  it('closes a frame whose processing throws', async () => {
    const s = frameStream(async () => {
      throw new Error('broken');
    });
    const given = frame(1);
    await s.writable.getWriter().write(asFrame(given));
    expect(given.closed).toBe(true);
  });

  it('closes the frame waiting when the writing is aborted', async () => {
    const out = frame(9);
    const s = frameStream(async (input, c) => {
      c.enqueue(asFrame(out));
      input.close();
    });
    const writer = s.writable.getWriter();
    void writer.write(asFrame(frame(1))).catch(() => undefined);
    await new Promise((r) => setTimeout(r, 0));
    await writer.abort().catch(() => undefined);
    expect(out.closed).toBe(true);
  });
});
