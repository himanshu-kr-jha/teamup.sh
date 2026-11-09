// The processor your camera's picture goes through when you have chosen a
// background or a colour filter. Loaded only when one is chosen (background.ts),
// so the processor and MediaPipe are fetched by nobody else.
//
// It is LiveKit's background processor with two changes:
//  - A colour filter is applied to every frame it puts out, background or not.
//    One processor per camera is all LiveKit allows, so the filter has to ride in
//    the same one.
//  - The first frame goes out untouched while MediaPipe warms up, which takes a
//    moment or, on a computer without a graphics card, a few seconds, so the
//    camera never goes black meanwhile. The library does this too, but then waits
//    for a callback from a video element nobody sees, which a browser need not
//    ever make; this pauses briefly instead.
//  - It says when a frame has gone out, so the picker can show it is working
//    until the change is really on screen.

import { BackgroundProcessorWrapper, BackgroundTransformer, type BackgroundOptions } from '@livekit/track-processors';

type InitOptions = Parameters<BackgroundTransformer['init']>[0];

class TeamUpTransformer extends BackgroundTransformer {
  /** A canvas filter, as CSS writes it ('grayscale(1)'), and a colour washed over in soft light; both '' for none. */
  look: Look = { filter: '', wash: '' };
  private lookCanvas?: OffscreenCanvas;
  private lookContext?: OffscreenCanvasRenderingContext2D | null;

  private waiting: (() => void)[] = [];

  /**
   * Sets MediaPipe up and runs it once on a blank picture, before the camera is
   * switched over to this processor. Its first run is slow, from a fraction of a
   * second to a few seconds on a computer without a graphics card, and holds the
   * page while it lasts; done here, your camera goes on showing as it is meanwhile,
   * instead of going black and then freezing.
   */
  async init(options: InitOptions) {
    await super.init(options);
    try {
      const blank = new ImageData(256, 256);
      this.imageSegmenter?.segmentForVideo(blank, performance.now(), (result) => result.close());
    } catch {
      // Not warmed up; the first real frame does it instead.
    }
  }

  /** Resolves once the next frame has gone out, with whatever is set now. */
  nextFrame(): Promise<void> {
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
      if (this.isFirstFrame) {
        this.isFirstFrame = false;
        // The picture as it is, at once, then a moment for it to be drawn
        // before the first, slow, run of the segmenter holds everything up.
        controller.enqueue(this.look.filter || this.look.wash ? this.filtered(frame.clone()) : frame.clone());
        await new Promise((r) => setTimeout(r, 60));
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

/** A new processor, set up for this background and look. */
export function createCameraProcessor(background: BackgroundSetting, look: Look, assets: { tasksVisionFileSet: string; modelAssetPath: string }) {
  const transformer = new TeamUpTransformer({ ...backgroundOptions(background), assetPaths: assets });
  transformer.look = look;
  return new BackgroundProcessorWrapper(transformer, PROCESSOR_NAME);
}

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
  transformer.look = look;
  await processor.updateTransformerOptions(backgroundOptions(background) as BackgroundOptions);
  await transformer.nextFrame();
}

/** Resolves once a new processor has put out a frame with the effect on it. */
export async function shown(processor: BackgroundProcessorWrapper) {
  const transformer = processor.transformer as TeamUpTransformer;
  // The first frame goes out untouched; the one after it carries the effect.
  await transformer.nextFrame();
  await transformer.nextFrame();
}
