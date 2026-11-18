// RNNoise on your microphone, on this device: a small neural network that keeps
// voices and takes out steady and sudden noise alike (fans, traffic, keyboards).
// Loaded only when noise cancellation is on (noiseCancellation.ts), so its
// WebAssembly and worklet are fetched by nobody else, and always from this
// server: the files are part of the build, never a CDN.
//
// The network works on 10 ms frames of 48 kHz audio, so the microphone is run
// through an AudioContext of its own at that rate rather than the call's, which
// is at whatever rate the device prefers.

import { RnnoiseWorkletNode, loadRnnoise } from '@sapphi-red/web-noise-suppressor';
import workletUrl from '@sapphi-red/web-noise-suppressor/rnnoiseWorklet.js?url';
import wasmUrl from '@sapphi-red/web-noise-suppressor/rnnoise.wasm?url';
import simdWasmUrl from '@sapphi-red/web-noise-suppressor/rnnoise_simd.wasm?url';
import type { AudioProcessorOptions, Track, TrackProcessor } from 'livekit-client';

/** The processor's name, which is also how a track is known to have it. */
export const RNNOISE_PROCESSOR = 'teamup-rnnoise';

const SAMPLE_RATE = 48_000;

// Fetched once for the page: every microphone after the first starts at once.
let wasm: Promise<ArrayBuffer> | null = null;
function wasmBinary(): Promise<ArrayBuffer> {
  wasm ??= loadRnnoise({ url: wasmUrl, simdUrl: simdWasmUrl }).catch((err) => {
    wasm = null;
    throw err;
  });
  return wasm;
}

/** A microphone track run through RNNoise, and how to let it all go. */
export interface Denoised {
  track: MediaStreamTrack;
  context: AudioContext;
  close: () => void;
}

/**
 * Runs one microphone track through RNNoise. Throws where the browser cannot:
 * no AudioWorklet, no WebAssembly, or a context that will not take the
 * microphone at 48 kHz (older Firefox), and the caller falls back to the
 * browser's own filter.
 */
export async function denoise(input: MediaStreamTrack): Promise<Denoised> {
  const context = new AudioContext({ sampleRate: SAMPLE_RATE, latencyHint: 'interactive' });
  try {
    const [binary] = await Promise.all([wasmBinary(), context.audioWorklet.addModule(workletUrl)]);
    const source = context.createMediaStreamSource(new MediaStream([input]));
    const node = new RnnoiseWorkletNode(context, { maxChannels: 1, wasmBinary: binary });
    const out = context.createMediaStreamDestination();
    out.channelCount = 1;
    source.connect(node).connect(out);
    // A page nobody has touched since it loaded starts it suspended, and it stays
    // so until they do. Sending its silence would mute you, so the caller is told.
    await Promise.race([context.resume().catch(() => undefined), new Promise((r) => setTimeout(r, 500))]);
    if (context.state !== 'running') throw new DOMException('suspended', 'NotAllowedError');
    const [track] = out.stream.getAudioTracks();
    if (!track) throw new Error('no processed track');
    let closed = false;
    return {
      track,
      context,
      close: () => {
        if (closed) return;
        closed = true;
        source.disconnect();
        node.disconnect();
        node.destroy();
        track.stop();
        void context.close().catch(() => undefined);
      },
    };
  } catch (err) {
    void context.close().catch(() => undefined);
    throw err;
  }
}

/**
 * RNNoise as a LiveKit processor on the call's microphone track: what is sent is
 * the cleaned track. LiveKit restarts it with the new microphone when you switch
 * devices, and muting silences the microphone underneath it.
 */
export class RnnoiseProcessor implements TrackProcessor<Track.Kind.Audio, AudioProcessorOptions> {
  name = RNNOISE_PROCESSOR;
  processedTrack?: MediaStreamTrack;
  private running?: Denoised;

  async init(opts: AudioProcessorOptions): Promise<void> {
    this.running = await denoise(opts.track);
    this.processedTrack = this.running.track;
  }

  async restart(opts: AudioProcessorOptions): Promise<void> {
    await this.destroy();
    await this.init(opts);
  }

  async destroy(): Promise<void> {
    this.running?.close();
    this.running = undefined;
    this.processedTrack = undefined;
  }
}

export const createRnnoiseProcessor = () => new RnnoiseProcessor();
