// A stream of camera frames that never leaves one unclosed (cameraProcessor.ts).

/**
 * A stream that runs each frame through `transform`, like a TransformStream, but
 * that closes every frame it holds when it is torn down. A TransformStream keeps
 * what it has put out in a queue, and when what reads it goes away (the processor
 * taken off, the camera turned off) drops that queue without closing the frames,
 * which the browser then reports and, until they are collected, holds camera
 * memory for. Here at most one frame waits, and it is closed on cancel. On the
 * writing side the frame being worked on is the only one in hand: a writable
 * stream takes no next frame while one is being written.
 */
export function frameStream(
  transform: (frame: VideoFrame, controller: TransformStreamDefaultController<VideoFrame>) => Promise<void>
): { writable: WritableStream<VideoFrame>; readable: ReadableStream<VideoFrame> } {
  /** A frame put out and not yet read: at most one. */
  let waiting: VideoFrame | null = null;
  /** Nobody reads any more: whatever comes is closed. */
  let cancelled = false;
  /** Nothing more is written: the reading side ends once it has what is waiting. */
  let ended = false;
  let wake: (() => void) | null = null;
  let taken: (() => void) | null = null;
  const signal = () => {
    wake?.();
    wake = null;
  };
  const release = () => {
    taken?.();
    taken = null;
  };
  const controller = {
    enqueue: (frame: VideoFrame) => {
      if (cancelled) {
        frame.close();
        return;
      }
      // Only the newest is kept: one not yet read is out of date.
      waiting?.close();
      waiting = frame;
      signal();
    },
    error: () => undefined,
    terminate: () => undefined,
    desiredSize: 1,
  } as unknown as TransformStreamDefaultController<VideoFrame>;
  const readable = new ReadableStream<VideoFrame>(
    {
      pull: async (out) => {
        while (!waiting && !ended && !cancelled) await new Promise<void>((r) => (wake = r));
        if (cancelled) return;
        if (waiting) {
          out.enqueue(waiting);
          waiting = null;
          release();
        } else {
          out.close();
        }
      },
      cancel: () => {
        cancelled = true;
        waiting?.close();
        waiting = null;
        signal();
        release();
      },
    },
    { highWaterMark: 0 }
  );
  // Fires as soon as the writing is aborted, even while a frame waits to be read.
  let aborted: AbortSignal | undefined;
  const giveUp = () => {
    ended = true;
    waiting?.close();
    waiting = null;
    signal();
    release();
  };
  const writable = new WritableStream<VideoFrame>(
    {
      start: (c) => {
        aborted = c.signal;
        aborted.addEventListener('abort', giveUp, { once: true });
      },
      write: async (frame) => {
        if (cancelled) {
          frame.close();
          return;
        }
        try {
          await transform(frame, controller);
        } catch {
          frame.close();
        }
        // The next frame waits until this one's result has been read, as a
        // TransformStream would make it.
        if (waiting && !cancelled && !aborted?.aborted) await new Promise<void>((r) => (taken = r));
      },
      close: () => {
        ended = true;
        signal();
      },
      abort: giveUp,
    },
    new CountQueuingStrategy({ highWaterMark: 1 })
  );
  return { writable, readable };
}
