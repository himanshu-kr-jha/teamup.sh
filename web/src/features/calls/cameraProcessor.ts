// The processor your camera's picture goes through when you have chosen a
// background or a colour filter. Loaded only when one is chosen (background.ts),
// so the processor and MediaPipe are fetched by nobody else.
//
// It is LiveKit's background processor with these changes:
//  - A colour filter is applied to every frame it puts out, background or not.
//    One processor per camera is all LiveKit allows, so the filter has to ride in
//    the same one.
//  - It is made ready before it is put on the camera, as Google Meet does: MediaPipe
//    and its model loaded, and one blank picture run through all of it at the
//    camera's size, which is the slow part (a second or two on a computer without
//    a graphics card). Meanwhile the camera goes on sending as it is; only once the
//    processor is ready does it take over, in one step. Before, it took over first
//    and got ready after, and the video stood still for everybody until it was.
//  - The one made ready can show itself first on a copy of the camera, in the
//    picker's preview, and is then handed to the camera itself (openPreview).
//  - It keeps MediaPipe when LiveKit restarts it, as it does when the camera comes
//    back on or another one is picked, so that is quick as well.
//  - It says when a frame has gone out, so the picker can show it is working
//    until the change is really on screen.

import { FilesetResolver } from '@mediapipe/tasks-vision';
import {
  BackgroundProcessorWrapper,
  BackgroundTransformer,
  supportsBackgroundProcessors,
  type BackgroundOptions,
} from '@livekit/track-processors';

type InitOptions = Parameters<BackgroundTransformer['init']>[0];
type DestroyOptions = Parameters<BackgroundTransformer['destroy']>[0];

/** Where MediaPipe's runtime and the model are: served by TeamUp itself (vite.config.ts). */
const ASSETS = {
  tasksVisionFileSet: __MEDIAPIPE_WASM__,
  modelAssetPath: '/mediapipe/selfie_segmenter.tflite',
};

/** The longest the camera waits for the preview to show a change before taking it anyway. */
const PREVIEW_WAIT_MS = 2_000;

/** How long a processor made ready and no longer wanted is kept, in case it is wanted again at once. */
const SPARE_LINGER_MS = 1_500;

/** The size a processor is made ready at when the camera's own is not known yet. */
const DEFAULT_SIZE = { width: 1280, height: 720 };

class TeamUpTransformer extends BackgroundTransformer {
  /** A canvas filter, as CSS writes it ('grayscale(1)'), and a colour washed over in soft light; both '' for none. */
  look: Look = { filter: '', wash: '' };
  private lookCanvas?: OffscreenCanvas;
  private lookContext?: OffscreenCanvasRenderingContext2D | null;

  /** MediaPipe and the drawing are set up and have run once: frames can come. */
  private ready = false;
  /** Called when a camera starts sending its frames through, made ready or not. */
  onAttach?: () => void;
  private waiting: (() => void)[] = [];

  /**
   * Sets everything up before any camera is attached, at the camera's size, and
   * runs a blank picture through it once. Every first run is slow and holds the
   * page while it lasts; done here, the camera goes on showing as it is meanwhile.
   */
  async prepare(width: number, height: number) {
    await super.init({ outputCanvas: new OffscreenCanvas(width, height), inputElement: document.createElement('video') });
    this.warmUp(width, height, true);
    this.ready = true;
  }

  /**
   * Called by LiveKit when the processor goes on a camera, and again when it is
   * restarted. One already made ready only needs a new stream for this camera.
   */
  async init(options: InitOptions) {
    if (!this.ready || !this.imageSegmenter) {
      await super.init(options);
      const video = options.inputElement;
      this.warmUp(video.videoWidth || DEFAULT_SIZE.width, video.videoHeight || DEFAULT_SIZE.height, true);
      this.ready = true;
    } else {
      this.inputVideo = options.inputElement;
      this.isDisabled = false;
    }
    this.transformer = new TransformStream({ transform: (frame, controller) => this.transform(frame, controller) });
    this.onAttach?.();
  }

  /**
   * A restart (the camera back on, or another camera) keeps MediaPipe and the
   * drawing; only the stream goes, and init makes a new one. Anything else lets
   * it all go.
   */
  async destroy(options?: DestroyOptions) {
    if (options?.willProcessorRestart && this.ready) {
      this.isDisabled = true;
      return;
    }
    this.ready = false;
    this.waiting.splice(0).forEach((r) => r());
    await super.destroy(options);
  }

  /**
   * Changes the background and the look together. A picture is loaded first and
   * only then switched to, so the frames meanwhile keep the effect they had.
   */
  async set(background: BackgroundSetting, look: Look) {
    const options = backgroundOptions(background);
    if (options.imagePath) await this.loadPicture(options.imagePath);
    this.look = look;
    await this.update(options as BackgroundOptions);
  }

  /** Loads a background picture and keeps it, without putting it on yet. */
  private async loadPicture(path: string) {
    if (this.backgroundImageAndPath?.path === path) return;
    const img = new Image();
    img.crossOrigin = 'anonymous';
    img.src = path;
    await img.decode();
    const imageData = await createImageBitmap(img);
    // update() finds it here and puts it on at once.
    this.backgroundImageAndPath = { imageData, path };
  }

  /**
   * Runs a blank picture of this size through the drawing, and through MediaPipe
   * too when asked, so the first real frame does not pay for setting them up.
   */
  warmUp(width: number, height: number, segment: boolean) {
    try {
      const blank = new OffscreenCanvas(width, height);
      const ctx = blank.getContext('2d');
      if (ctx) {
        ctx.fillStyle = '#7f7f7f';
        ctx.fillRect(0, 0, width, height);
      }
      const frame = new VideoFrame(blank, { timestamp: 0 });
      try {
        if (this.canvas) {
          this.canvas.width = width;
          this.canvas.height = height;
        }
        if (segment) {
          this.imageSegmenter?.segmentForVideo(frame, performance.now(), (result) => {
            const mask = result.categoryMask;
            if (mask) this.gl?.updateMask(mask.getAsWebGLTexture());
            result.close();
          });
        }
        this.gl?.renderFrame(frame);
        if (this.canvas) new VideoFrame(this.canvas, { timestamp: 0 }).close();
      } finally {
        frame.close();
      }
    } catch {
      // Not warmed up; the first real frame does it instead.
    }
    // Nothing slow is left for the first frame, so it need not go out untouched first.
    this.isFirstFrame = false;
  }

  /** Resolves once the next frame has gone out, with whatever is set now. */
  nextFrame(): Promise<void> {
    if (!this.ready) return Promise.resolve();
    return new Promise((resolve) => this.waiting.push(resolve));
  }

  async transform(frame: VideoFrame, given: TransformStreamDefaultController<VideoFrame>) {
    // A frame still on its way when the effect is taken off has nowhere to go:
    // the stream is closed under it. Dropped quietly rather than reported.
    const controller = {
      enqueue: (out: VideoFrame) => {
        if (this.isDisabled) {
          out.close();
          return;
        }
        try {
          given.enqueue(out);
        } catch {
          out.close();
        }
      },
      error: (reason?: unknown) => given.error(reason),
      terminate: () => given.terminate(),
      get desiredSize() {
        return given.desiredSize;
      },
    } as TransformStreamDefaultController<VideoFrame>;
    try {
      if (this.isDisabled) {
        frame.close();
        return;
      }
      await this.process(frame, controller);
    } finally {
      const done = this.waiting;
      this.waiting = [];
      done.forEach((r) => r());
    }
  }

  private async process(frame: VideoFrame, controller: TransformStreamDefaultController<VideoFrame>) {
    if (!this.look.filter && !this.look.wash) return super.transform(frame, controller);
    const filtered = {
      enqueue: (out: VideoFrame) => controller.enqueue(this.filtered(out)),
      error: (reason?: unknown) => controller.error(reason),
      terminate: () => controller.terminate(),
      get desiredSize() {
        return controller.desiredSize;
      },
    } as TransformStreamDefaultController<VideoFrame>;
    return super.transform(frame, filtered);
  }

  /** The frame through the filter, as a new frame; the one given is closed. */
  private filtered(frame: VideoFrame): VideoFrame {
    const width = frame.displayWidth;
    const height = frame.displayHeight;
    try {
      if (!this.lookCanvas || this.lookCanvas.width !== width || this.lookCanvas.height !== height) {
        this.lookCanvas = new OffscreenCanvas(width, height);
        this.lookContext = this.lookCanvas.getContext('2d');
      }
      const ctx = this.lookContext;
      if (!ctx) return frame;
      ctx.filter = this.look.filter || 'none';
      ctx.drawImage(frame, 0, 0, width, height);
      if (this.look.wash) {
        ctx.filter = 'none';
        ctx.globalCompositeOperation = 'soft-light';
        ctx.fillStyle = this.look.wash;
        ctx.fillRect(0, 0, width, height);
        ctx.globalCompositeOperation = 'source-over';
      }
      const out = new VideoFrame(this.lookCanvas, { timestamp: frame.timestamp });
      frame.close();
      return out;
    } catch {
      // A frame that cannot be filtered goes out as it is, rather than not at all.
      return frame;
    }
  }
}

/** A look: a canvas filter and a colour washed over in soft light. */
export type Look = { filter: string; wash: string };

export type BackgroundSetting = { blurRadius: number } | { imagePath: string } | null;

function backgroundOptions(background: BackgroundSetting): Partial<BackgroundOptions> {
  if (!background) return { blurRadius: undefined, imagePath: undefined, backgroundDisabled: true };
  if ('blurRadius' in background) return { blurRadius: background.blurRadius, imagePath: undefined, backgroundDisabled: false };
  return { imagePath: background.imagePath, blurRadius: undefined, backgroundDisabled: false };
}

export const PROCESSOR_NAME = 'teamup-camera';

/** Whether this is a processor made here, which can be changed in place. */
export function isCameraProcessor(processor: unknown): processor is BackgroundProcessorWrapper {
  return (
    processor instanceof BackgroundProcessorWrapper &&
    processor.name === PROCESSOR_NAME &&
    processor.transformer instanceof TeamUpTransformer
  );
}

/** Changes a running processor in place, no restart and so no flicker, and resolves once it shows. */
export async function updateCameraProcessor(processor: BackgroundProcessorWrapper, background: BackgroundSetting, look: Look) {
  const transformer = processor.transformer as TeamUpTransformer;
  await transformer.set(background, look);
  await transformer.nextFrame();
}

/** The camera itself, as it is, under a processor: what it reads its frames from. */
export function cameraUnder(processor: BackgroundProcessorWrapper): MediaStreamTrack | undefined {
  return processor.source;
}

// ── Made ready ahead ──────────────────────────────────────────────────────

// The browser's own stream API, which TypeScript's DOM types do not have yet.
type FrameReader = { readable: ReadableStream<VideoFrame> };
type FrameWriter = MediaStreamTrack & { writable: WritableStream<VideoFrame> };
const streams = globalThis as unknown as {
  MediaStreamTrackProcessor?: new (init: { track: MediaStreamTrack }) => FrameReader;
  MediaStreamTrackGenerator?: new (init: { kind: 'video' }) => FrameWriter;
};

interface Pipe {
  track: MediaStreamTrack;
  /** Stops running frames through, the preview keeping the last one. */
  pause(): void;
  stop(): void;
}

/**
 * A processor being made ready, not yet on any camera. There is at most one: the
 * picker's preview and the camera share it, so what the preview has made ready
 * is what the camera then takes.
 */
class Spare {
  readonly transformer: TeamUpTransformer;
  readonly ready: Promise<void>;
  holders = 0;
  /** Handed to a camera: it is that camera's now, and goes with it. */
  taken = false;
  /** Copies of the camera it is showing itself on, in a preview. */
  readonly pipes = new Set<Pipe>();
  private noPipes: (() => void)[] = [];

  constructor(width: number, height: number) {
    this.transformer = new TeamUpTransformer({ ...backgroundOptions(null), assetPaths: ASSETS });
    this.ready = this.transformer.prepare(width, height);
  }

  /**
   * Resolves once the preview has shown what is set now; or at once when the
   * preview is closed meanwhile, or no frame comes for a while, since the camera
   * itself is waiting on it.
   */
  previewed(): Promise<void> {
    if (this.pipes.size === 0) return Promise.resolve();
    return new Promise((resolve) => {
      const done = () => {
        clearTimeout(timer);
        resolve();
      };
      const timer = setTimeout(done, PREVIEW_WAIT_MS);
      void this.transformer.nextFrame().then(done);
      this.noPipes.push(done);
    });
  }

  /**
   * Runs a copy of the camera through it, for a preview; null where the browser
   * cannot. The copy is the preview's own, and is stopped with it.
   */
  pipe(source: MediaStreamTrack): Pipe | null {
    const { MediaStreamTrackProcessor: Reader, MediaStreamTrackGenerator: Writer } = streams;
    if (!Reader || !Writer) return null;
    const frames = new Reader({ track: source }).readable.getReader();
    const writer = new Writer({ kind: 'video' });
    const out = writer.writable.getWriter();
    // Frame by frame rather than through piped streams, and to the end of the copy
    // rather than cancelled, so that no frame is ever left in a queue unclosed.
    let feeding = true;
    const controller = {
      enqueue: (frame: VideoFrame) => void out.write(frame).catch(() => frame.close()),
      error: () => undefined,
      terminate: () => undefined,
      desiredSize: 1,
    } as unknown as TransformStreamDefaultController<VideoFrame>;
    void (async () => {
      for (;;) {
        const { value: frame, done } = await frames.read().catch(() => ({ value: undefined, done: true as const }));
        if (done || !frame) return;
        if (feeding) await this.transformer.transform(frame, controller);
        else frame.close();
      }
    })();
    const pipe: Pipe = {
      track: writer,
      // The preview stays on its last frame rather than going blank.
      pause: () => (feeding = false),
      stop: () => {
        if (!this.pipes.delete(pipe)) return;
        feeding = false;
        // The copy is the preview's alone; once it ends, so does the reading.
        source.stop();
        writer.stop();
        if (this.pipes.size === 0) this.noPipes.splice(0).forEach((r) => r());
      },
    };
    this.pipes.add(pipe);
    return pipe;
  }
}

let spare: Spare | null = null;

interface Lease {
  spare: Spare;
  release(): void;
}

/** The processor being made ready, made now if there is none; let go of it with release(). */
function leaseSpare(width = DEFAULT_SIZE.width, height = DEFAULT_SIZE.height): Lease {
  if (!spare) {
    const made = new Spare(width, height);
    spare = made;
    // One that could not be made is not kept for the next to try.
    made.ready.catch(() => {
      if (spare === made) spare = null;
    });
  }
  const held = spare;
  held.holders++;
  let released = false;
  return {
    spare: held,
    release() {
      if (released) return;
      released = true;
      held.holders--;
      if (held.holders > 0 || held.taken) return;
      held.pipes.forEach((p) => p.stop());
      // Kept a moment, for the choice made just before the picker closed.
      setTimeout(() => {
        if (held.holders > 0 || held.taken) return;
        if (spare === held) spare = null;
        held.ready.then(
          () => held.transformer.destroy(),
          () => undefined
        );
      }, SPARE_LINGER_MS);
    },
  };
}

/**
 * Puts a background and look on a camera that has no processor yet, without the
 * video standing still: the processor is made ready first (or taken from the
 * picker's preview, which has made it ready already) while the camera sends as
 * it is, and is then put on in one step. Resolves once it shows. `isLive` says
 * whether the camera is still on to take it.
 */
export async function attachCameraProcessor(
  put: (processor: BackgroundProcessorWrapper) => Promise<void>,
  background: BackgroundSetting,
  look: Look,
  size: { width?: number; height?: number },
  isLive: () => boolean
): Promise<void> {
  const lease = leaseSpare(size.width, size.height);
  try {
    const made = lease.spare;
    const { transformer } = made;
    await made.ready;
    await transformer.set(background, look);
    if (made.pipes.size > 0) {
      // Seen in the preview first, so the switch shows what is already there.
      await made.previewed();
    } else {
      // Nobody is looking: the drawing is warmed for this effect instead.
      transformer.warmUp(size.width || DEFAULT_SIZE.width, size.height || DEFAULT_SIZE.height, false);
    }
    if (!isLive() || made.taken) return;
    made.taken = true;
    if (spare === made) spare = null;
    const processor = new BackgroundProcessorWrapper(transformer, PROCESSOR_NAME);
    // From the moment the camera's frames start coming through, the preview's copy
    // waits on its last frame: two pictures through one processor would halve how
    // often each gets one, and the camera's are the ones everybody sees. The
    // picker shows the camera's own picture once it carries the effect.
    transformer.onAttach = () => {
      transformer.onAttach = undefined;
      made.pipes.forEach((p) => p.pause());
    };
    try {
      await put(processor);
    } catch (err) {
      made.pipes.forEach((p) => p.stop());
      await transformer.destroy();
      throw err;
    }
    await transformer.nextFrame();
  } finally {
    lease.release();
  }
}

/** A preview of the effects on a copy of the camera, before the camera itself has them. */
export interface Preview {
  /** The copy through the processor, once it is ready; null where this browser cannot show it. */
  output: Promise<MediaStreamTrack | null>;
  /** Stops the preview; the processor goes too, unless the camera has taken it. */
  close(): void;
}

/**
 * Shows a copy of the camera (never the one sent) through the processor being
 * made ready, with whatever is chosen. When a background is then chosen, the
 * camera takes this same processor once the preview shows it (attachCameraProcessor).
 */
export function openPreview(source: MediaStreamTrack, size: { width?: number; height?: number }): Preview {
  const lease = leaseSpare(size.width, size.height);
  let closed = false;
  let pipe: Pipe | null = null;
  const output = lease.spare.ready
    .then(() => {
      if (closed || lease.spare.taken || source.readyState !== 'live') return null;
      pipe = lease.spare.pipe(source);
      return pipe?.track ?? null;
    })
    .catch(() => null);
  return {
    output,
    close() {
      closed = true;
      pipe?.stop();
      lease.release();
    },
  };
}

let preloading: Promise<void> | null = null;

/**
 * Fetches MediaPipe's runtime and the model into the browser's cache, so the first
 * background is quick. Kept for a year (cmd/teamup/static.go), so this is once.
 */
export function preloadEffects(): Promise<void> {
  preloading ??= (async () => {
    if (!supportsBackgroundProcessors()) return;
    const files = await FilesetResolver.forVisionTasks(ASSETS.tasksVisionFileSet);
    const urls = [files.wasmLoaderPath, files.wasmBinaryPath, ASSETS.modelAssetPath];
    await Promise.all(urls.map((url) => fetch(url, { priority: 'low' } as RequestInit).then((r) => r.arrayBuffer())));
  })().catch(() => {
    preloading = null;
  });
  return preloading;
}
