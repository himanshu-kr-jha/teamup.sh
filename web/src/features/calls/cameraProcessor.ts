// The processor your camera's picture goes through when you have chosen a
// background or a colour filter. Loaded only when one is chosen (background.ts),
// so the processor and MediaPipe are fetched by nobody else.
//
// It is LiveKit's background transformer, run frame by frame by TeamUp itself
// (runFrames, CameraFramesProcessor) so that every frame is closed, with these
// changes:
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
//  - It is kept, made ready, when its camera stops, for as long as a background
//    or a look is chosen: the preview's camera stopping on Join, or a call
//    ending, hands it to the next camera instead of throwing away what took a
//    second or more to set up (keepEffects).
//  - It can go on a camera before the camera is sent at all (processorForNewCamera),
//    so somebody who has chosen a background never sends the room behind them,
//    not even its first frame.
//  - It says when a frame has gone out, so the picker can show it is working
//    until the change is really on screen.

import { FilesetResolver } from '@mediapipe/tasks-vision';
import type { ProcessorOptions, Track } from 'livekit-client';
import { frameStream } from './frameStream';
// Named by its content when built, so the server lets browsers keep it for good.
import modelUrl from './assets/selfie_segmenter.tflite?url';
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
  modelAssetPath: modelUrl,
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
  /** Setting it up, once: whoever asks while it is under way waits for the same. */
  private preparing?: Promise<void>;
  /** Let go of for good: MediaPipe closed. Once is enough, whoever asks. */
  private gone = false;
  /** Called when a camera starts sending its frames through, made ready or not. */
  onAttach?: () => void;
  private waiting: (() => void)[] = [];

  /**
   * Sets everything up before any camera is attached, at the camera's size, and
   * runs a blank picture through it once. Every first run is slow and holds the
   * page while it lasts; done here, the camera goes on showing as it is meanwhile.
   */
  prepare(width: number, height: number): Promise<void> {
    this.preparing ??= (async () => {
      // A download of the same files already under way is waited for: two at
      // once are both fetched in full, as neither is in the cache yet, and the
      // runtime is 9 MB.
      await preloading;
      await super.init({ outputCanvas: new OffscreenCanvas(width, height), inputElement: document.createElement('video') });
      this.warmUp(width, height, true);
      this.ready = true;
    })();
    return this.preparing;
  }

  /** Takes frames again, as one handed on by a camera that stopped does not. */
  takeFrames() {
    this.isDisabled = false;
  }

  /**
   * What has to be done before a camera's frames may come: for one put on a
   * camera before it is sent, its background set. Awaited by attach, so no frame
   * goes out without it.
   */
  before?: Promise<void>;

  /**
   * Readies it for a camera's frames, made ready first if it was not (it always
   * is when it comes from attachCameraProcessor), and says so to onAttach.
   */
  async attach(size: { width: number; height: number }, inputElement: HTMLVideoElement) {
    if (this.gone) throw new Error('This camera processor has been let go of.');
    await this.prepare(size.width, size.height);
    await this.before;
    this.inputVideo = inputElement;
    this.isDisabled = false;
    this.onAttach?.();
  }

  /**
   * Called by LiveKit's own processor (in a browser that cannot run frames one by
   * one, see CameraFramesProcessor) when it goes on a camera, and again when it
   * is restarted. One already made ready only needs a new stream for this camera.
   */
  async init(options: InitOptions) {
    const video = options.inputElement;
    await this.attach({ width: video.videoWidth || DEFAULT_SIZE.width, height: video.videoHeight || DEFAULT_SIZE.height }, video);
    // LiveKit pipes the camera through this; frameStream rather than a
    // TransformStream, which drops queued frames unclosed when it is torn down.
    this.transformer = frameStream((frame, controller) => this.transform(frame, controller)) as unknown as TransformStream;
  }

  /**
   * A restart (the camera back on, or another camera) keeps MediaPipe and the
   * drawing; only the stream goes, and init makes a new one. A camera that stops
   * hands it on, made ready, to the next one while effects are wanted (park).
   * Anything else lets it all go.
   */
  async destroy(options?: DestroyOptions) {
    if (options?.willProcessorRestart && this.ready) {
      this.isDisabled = true;
      return;
    }
    this.isDisabled = true;
    this.onAttach = undefined;
    this.before = undefined;
    this.waiting.splice(0).forEach((r) => r());
    if (this.ready && !this.gone && park(this)) return;
    await this.dispose(options);
  }

  /** Lets MediaPipe and the drawing go, for good. */
  async dispose(options?: DestroyOptions) {
    if (this.gone) return;
    this.gone = true;
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

/** Whether this browser can run a camera's frames one by one (runFrames). */
const canRunFrames = () =>
  typeof VideoFrame !== 'undefined' && typeof Worker !== 'undefined' && typeof HTMLCanvasElement.prototype.captureStream === 'function';

/** How often the copy is looked at for a new frame: twice a 30 fps camera's rate. */
const TICK_MS = 1000 / 60;

/** The rest left after each frame, as a share of the time it took (runFrames). */
const REST_SHARE = 0.5;

/**
 * Calls `onTick` every `ms`, from a worker's timer: a hidden tab's own timers
 * slow to once a second, and a video in it shows no frames to wait for, which
 * would leave everybody else with a picture that hardly moves.
 */
function ticker(ms: number, onTick: () => void): () => void {
  const url = URL.createObjectURL(new Blob(['onmessage = (e) => setTimeout(() => postMessage(0), e.data);'], { type: 'text/javascript' }));
  let worker: Worker | null = null;
  try {
    worker = new Worker(url);
  } catch {
    // No worker: a window timer, slow while hidden but never stopped.
  } finally {
    URL.revokeObjectURL(url);
  }
  if (!worker) {
    const id = setInterval(onTick, ms);
    return () => clearInterval(id);
  }
  const w = worker;
  w.onmessage = () => {
    try {
      onTick();
    } finally {
      w.postMessage(ms);
    }
  };
  w.postMessage(ms);
  return () => w.terminate();
}

interface Frames {
  /** What comes out: the frames, processed. */
  track: MediaStreamTrack;
  /** Stops running frames through, the output keeping the last one. */
  pause(): void;
  /** Starts running frames through, for one made held. */
  go(): void;
  /** Stops for good, and resolves once the frame in hand, if any, has been closed. */
  stop(): Promise<void>;
}

/**
 * Runs the frames of `input`, a copy of the camera that is this run's own,
 * through the processor, one by one, to a new track.
 *
 * Every frame is made here and closed here. The copy plays in a video element,
 * and each new frame it has is taken from it as a VideoFrame and processed (which
 * closes it); what comes out is drawn to a canvas, whose capture is the output,
 * and closed at once. The browser's own frame streams (MediaStreamTrackProcessor
 * and MediaStreamTrackGenerator, as LiveKit's processor uses) make frames of
 * their own that are never handed over when a camera stops, and the browser then
 * reports them as "garbage collected without being closed", some seconds after a
 * call is left.
 */
function runFrames(transformer: TeamUpTransformer, input: MediaStreamTrack, held = false): Frames {
  const video = document.createElement('video');
  video.muted = true;
  video.playsInline = true;
  video.srcObject = new MediaStream([input]);
  void video.play().catch(() => undefined);

  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d', { alpha: false });
  const output = canvas.captureStream().getVideoTracks()[0];

  let feeding = !held;
  let stopped = false;
  let busy: Promise<void> = Promise.resolve();
  const controller = {
    enqueue: (frame: VideoFrame) => {
      try {
        if (feeding && ctx) {
          if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
            canvas.width = frame.displayWidth;
            canvas.height = frame.displayHeight;
          }
          ctx.drawImage(frame, 0, 0);
        }
      } finally {
        frame.close();
      }
    },
    error: () => undefined,
    terminate: () => undefined,
    desiredSize: 1,
  } as unknown as TransformStreamDefaultController<VideoFrame>;

  // One frame at a time: one shown while the last is still being worked on is
  // skipped, as the browser's own stream would drop it.
  let working = false;
  let last = -1;
  // And a rest after each, of half as long as it took. A frame holds the page
  // while it is worked on: about 10 ms with a graphics card, which this leaves
  // at the camera's own pace, but a few hundred without one, when back to back
  // they left the page no time for anything else: joining the call, starting
  // the microphone and its noise cancellation, answering a click.
  let restUntil = 0;
  const stopTicking = ticker(TICK_MS, () => {
    if (working || !feeding || stopped || video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA || video.videoWidth === 0) return;
    if (video.currentTime === last || performance.now() < restUntil) return;
    last = video.currentTime;
    let frame: VideoFrame;
    try {
      frame = new VideoFrame(video, { timestamp: Math.round(video.currentTime * 1e6) });
    } catch {
      return;
    }
    working = true;
    const started = performance.now();
    busy = (async () => {
      try {
        await transformer.transform(frame, controller);
      } catch {
        // transform closes what it is given; this is for a throw before it could.
        frame.close();
      } finally {
        working = false;
        const now = performance.now();
        restUntil = now + (now - started) * REST_SHARE;
      }
    })();
  });

  let stopping: Promise<void> | null = null;
  return {
    track: output,
    pause: () => {
      feeding = false;
    },
    go: () => {
      if (!stopped) feeding = true;
    },
    stop: () =>
      (stopping ??= (async () => {
        feeding = false;
        stopped = true;
        stopTicking();
        await busy;
        video.srcObject = null;
        input.stop();
        output.stop();
      })()),
  };
}

/** Cameras letting go of their processor right now, which may hand it on (park). */
const lettingGo = new Set<Promise<void>>();

/**
 * The processor LiveKit puts on the camera: the frames of a copy of the camera,
 * run through a processor made ready (runFrames). LiveKit restarts it with the
 * new camera when the camera comes back on or another is picked, and destroys it
 * when it is taken off or the camera stops.
 *
 * Given a transformer, or how to get one when the camera starts: one put on a
 * camera before it is sent takes the processor made ready only then, which may
 * be the one the preview's camera is still handing on.
 */
class CameraFramesProcessor {
  readonly name = PROCESSOR_NAME;
  processedTrack?: MediaStreamTrack;
  /** The camera as it is, under the processor. */
  source?: MediaStreamTrack;
  transformer?: TeamUpTransformer;
  private frames?: Frames;
  /** Sent in a call: LiveKit has published it (onPublish). */
  private published = false;

  /**
   * `holdUntilPublished`, for a camera opened in a call: no frame is worked on
   * until LiveKit has published it. Nobody would see them meanwhile, and on a
   * computer without a graphics card each one holds the page for a few hundred
   * milliseconds, which held up the publishing itself by seconds.
   */
  constructor(
    private readonly given: TeamUpTransformer | (() => Promise<TeamUpTransformer>),
    private readonly holdUntilPublished = false
  ) {
    if (given instanceof TeamUpTransformer) this.transformer = given;
  }

  async onPublish() {
    this.published = true;
    this.frames?.go();
  }

  async init(opts: ProcessorOptions<Track.Kind>) {
    this.source = opts.track;
    const transformer = (this.transformer ??= await (this.given as () => Promise<TeamUpTransformer>)());
    const settings = opts.track.getSettings();
    await transformer.attach(
      { width: settings.width || DEFAULT_SIZE.width, height: settings.height || DEFAULT_SIZE.height },
      opts.element instanceof HTMLVideoElement ? opts.element : document.createElement('video')
    );
    this.frames = runFrames(transformer, opts.track.clone(), this.holdUntilPublished && !this.published);
    this.processedTrack = this.frames.track;
  }

  async restart(opts: ProcessorOptions<Track.Kind>) {
    await this.frames?.stop();
    await this.init(opts);
  }

  async destroy() {
    const going = (async () => {
      await this.frames?.stop();
      this.frames = undefined;
      await this.transformer?.destroy();
    })();
    lettingGo.add(going);
    try {
      await going;
    } finally {
      lettingGo.delete(going);
    }
  }
}

type CameraProcessor = CameraFramesProcessor | BackgroundProcessorWrapper;

/** Whether this is a processor made here, which can be changed in place. */
export function isCameraProcessor(processor: unknown): processor is CameraProcessor & { transformer: TeamUpTransformer } {
  return (
    (processor instanceof CameraFramesProcessor || processor instanceof BackgroundProcessorWrapper) &&
    processor.name === PROCESSOR_NAME &&
    processor.transformer instanceof TeamUpTransformer
  );
}

/** Changes a running processor in place, no restart and so no flicker, and resolves once it shows. */
export async function updateCameraProcessor(
  processor: CameraProcessor & { transformer: TeamUpTransformer },
  background: BackgroundSetting,
  look: Look
) {
  const transformer = processor.transformer;
  await transformer.set(background, look);
  await transformer.nextFrame();
}

/** The camera itself, as it is, under a processor: what it reads its frames from. */
export function cameraUnder(processor: CameraProcessor): MediaStreamTrack | undefined {
  return processor.source;
}

// ── Made ready ahead ──────────────────────────────────────────────────────

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
  readonly pipes = new Set<Frames>();
  private noPipes: (() => void)[] = [];

  constructor(width: number, height: number, made?: TeamUpTransformer) {
    this.transformer = made ?? new TeamUpTransformer({ ...backgroundOptions(null), assetPaths: ASSETS });
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
  pipe(source: MediaStreamTrack): Frames | null {
    if (!canRunFrames()) return null;
    // One handed on from a camera that stopped takes frames again.
    this.transformer.takeFrames();
    const frames = runFrames(this.transformer, source);
    const pipe: Frames = {
      track: frames.track,
      pause: frames.pause,
      go: frames.go,
      stop: () => {
        if (this.pipes.delete(pipe) && this.pipes.size === 0) this.noPipes.splice(0).forEach((r) => r());
        return frames.stop();
      },
    };
    this.pipes.add(pipe);
    return pipe;
  }
}

let spare: Spare | null = null;

/**
 * Whether a background or a look is chosen, so a processor made ready is kept
 * for the next camera rather than let go of (background.ts says).
 */
let keep = false;

/**
 * Keeps the processor made ready from one camera to the next, or stops keeping
 * it and lets go of one nobody is using.
 */
export function keepEffects(on: boolean) {
  keep = on;
  if (on || !spare || spare.holders > 0 || spare.taken) return;
  const gone = spare;
  spare = null;
  gone.pipes.forEach((p) => void p.stop());
  gone.ready.then(
    () => gone.transformer.dispose(),
    () => undefined
  );
}

/**
 * Takes a processor whose camera has stopped as the one made ready for the next,
 * while effects are wanted and there is not one already. False lets it go.
 */
function park(transformer: TeamUpTransformer): boolean {
  if (!keep) return false;
  if (spare) return spare.transformer === transformer;
  spare = new Spare(DEFAULT_SIZE.width, DEFAULT_SIZE.height, transformer);
  return true;
}

/**
 * Starts making the processor ready now, if it is not already, so the next
 * camera finds it so: on the screen before a call, for somebody with a
 * background chosen.
 */
export function warmEffects(size: { width?: number; height?: number } = {}) {
  if (!supportsBackgroundProcessors()) return;
  leaseSpare(size.width, size.height).release();
}

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
      held.pipes.forEach((p) => void p.stop());
      // Kept a moment, for the choice made just before the picker closed; and
      // for good while effects are wanted, for the next camera.
      setTimeout(() => {
        if (held.holders > 0 || held.taken || (keep && spare === held)) return;
        if (spare === held) spare = null;
        held.ready.then(
          () => held.transformer.dispose(),
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
  put: (processor: CameraProcessor) => Promise<void>,
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
    // Frame by frame where the browser can (CameraFramesProcessor); LiveKit's own,
    // which draws through a canvas, where it cannot (Safari, Firefox).
    const processor: CameraProcessor = canRunFrames()
      ? new CameraFramesProcessor(transformer)
      : new BackgroundProcessorWrapper(transformer, PROCESSOR_NAME);
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
      made.pipes.forEach((p) => void p.stop());
      await transformer.dispose();
      throw err;
    }
    if (processor.source?.readyState !== 'live') {
      // The camera was stopped while this went on, as the preview's is on joining
      // a call: LiveKit has already let go of its processor, so not of this one.
      made.pipes.forEach((p) => void p.stop());
      await processor.destroy();
      return;
    }
    await transformer.nextFrame();
  } finally {
    lease.release();
  }
}

/**
 * A processor for a camera about to be opened, so that it is sent through it
 * from its very first frame: LiveKit puts it on the camera before publishing it
 * (createLocalTracks), and publishes only once init has resolved, which is once
 * the background is set and a blank picture has been run through it. Nothing is
 * sent meanwhile. It takes the processor made ready ahead, the one the preview's
 * camera has just handed on included, so in a second call this is quick.
 */
export function processorForNewCamera(
  background: BackgroundSetting,
  look: Look,
  size: { width?: number; height?: number } = {},
  holdUntilPublished = false
): CameraProcessor | null {
  if (!supportsBackgroundProcessors()) return null;
  const take = async () => {
    // The preview's camera stops as Join is pressed, and hands its processor on
    // a moment later: waited for, rather than making a second one.
    await Promise.all([...lettingGo]);
    const lease = leaseSpare(size.width, size.height);
    const made = lease.spare;
    made.taken = true;
    if (spare === made) spare = null;
    made.pipes.forEach((p) => void p.stop());
    lease.release();
    const { transformer } = made;
    try {
      await made.ready;
      await transformer.set(background, look);
      transformer.warmUp(size.width || DEFAULT_SIZE.width, size.height || DEFAULT_SIZE.height, false);
    } catch (err) {
      await transformer.dispose();
      throw err;
    }
    return transformer;
  };
  if (canRunFrames()) return new CameraFramesProcessor(take, holdUntilPublished);
  // LiveKit's own needs its transformer at once; it waits in attach for the rest.
  const transformer = new TeamUpTransformer({ ...backgroundOptions(null), assetPaths: ASSETS });
  transformer.before = transformer
    .prepare(size.width || DEFAULT_SIZE.width, size.height || DEFAULT_SIZE.height)
    .then(() => transformer.set(background, look));
  return new BackgroundProcessorWrapper(transformer, PROCESSOR_NAME);
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
  let pipe: Frames | null = null;
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
      void pipe?.stop();
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
