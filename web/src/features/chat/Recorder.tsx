import { useCallback, useEffect, useRef, useState } from 'react';
import { Camera, Check, Loader2, RotateCcw, Trash2, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { cn } from '@/shared/utils/cn';
import { VideoNotePlayer } from '@/features/files/VideoNotePlayer';

/** The longest voice message, and the longest video message. */
export const MAX_VOICE_SECONDS = 5 * 60;
export const MAX_VIDEO_SECONDS = 60;

/**
 * The first format this browser can record. Chrome and Firefox record WebM;
 * Safari records MP4. Both play everywhere TeamUp runs.
 */
export function recordingFormat(kind: 'audio' | 'video', supports: (type: string) => boolean): { mime: string; ext: string } | null {
  const candidates =
    kind === 'audio'
      ? ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4', 'audio/ogg;codecs=opus']
      : ['video/webm;codecs=vp9,opus', 'video/webm;codecs=vp8,opus', 'video/webm', 'video/mp4'];
  for (const type of candidates) {
    if (supports(type)) {
      const base = type.split(';')[0];
      return { mime: type, ext: base.split('/')[1] === 'mp4' ? 'mp4' : base.split('/')[1] };
    }
  }
  return null;
}

/** "Voice message 2026-11-08 14.05.webm" */
export function recordingName(kind: 'audio' | 'video', ext: string, when = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  const stamp = `${when.getFullYear()}-${pad(when.getMonth() + 1)}-${pad(when.getDate())} ${pad(when.getHours())}.${pad(when.getMinutes())}.${pad(when.getSeconds())}`;
  return `${kind === 'audio' ? 'Voice message' : 'Video message'} ${stamp}.${ext}`;
}

export function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, '0')}`;
}

function supported(type: string) {
  return typeof MediaRecorder !== 'undefined' && MediaRecorder.isTypeSupported(type);
}

/** Records from a stream until stopped or the limit, and hands back a file. */
function useRecording(kind: 'audio' | 'video', limit: number) {
  const recorder = useRef<MediaRecorder | null>(null);
  const chunks = useRef<Blob[]>([]);
  const timer = useRef<number>();
  const [seconds, setSeconds] = useState(0);
  const [recording, setRecording] = useState(false);
  const resolveStop = useRef<((file: File | null) => void) | null>(null);

  const start = useCallback(
    (stream: MediaStream) => {
      const format = recordingFormat(kind, supported);
      if (!format) throw new Error('This browser cannot record. Try a recent Chrome, Edge, Firefox or Safari.');
      chunks.current = [];
      const r = new MediaRecorder(stream, { mimeType: format.mime });
      r.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.current.push(e.data);
      };
      r.onstop = () => {
        window.clearInterval(timer.current);
        setRecording(false);
        const type = format.mime.split(';')[0];
        const blob = new Blob(chunks.current, { type });
        resolveStop.current?.(blob.size > 0 ? new File([blob], recordingName(kind, format.ext), { type }) : null);
        resolveStop.current = null;
      };
      recorder.current = r;
      r.start(250);
      setSeconds(0);
      setRecording(true);
      const began = Date.now();
      timer.current = window.setInterval(() => {
        const s = (Date.now() - began) / 1000;
        setSeconds(s);
        if (s >= limit && r.state === 'recording') r.stop();
      }, 200);
    },
    [kind, limit]
  );

  const stop = useCallback(
    () =>
      new Promise<File | null>((resolve) => {
        const r = recorder.current;
        if (!r || r.state === 'inactive') return resolve(null);
        resolveStop.current = resolve;
        r.stop();
      }),
    []
  );

  /** Waits for the recorder to finish by itself, at the limit. */
  const finished = useCallback(
    () =>
      new Promise<File | null>((resolve) => {
        resolveStop.current = resolve;
      }),
    []
  );

  const discard = useCallback(() => {
    resolveStop.current = null;
    window.clearInterval(timer.current);
    if (recorder.current && recorder.current.state !== 'inactive') recorder.current.stop();
    setRecording(false);
  }, []);

  useEffect(() => () => window.clearInterval(timer.current), []);
  return { start, stop, finished, discard, seconds, recording };
}

function stopTracks(stream: MediaStream | null) {
  stream?.getTracks().forEach((t) => t.stop());
}

/** How many bars the live level shows. */
const LEVEL_BARS = 40;

/**
 * How loud the microphone is, as a row of recent levels from 0 to 1 that moves
 * along as you speak: proof it is hearing you, before you send silence.
 */
function useLevels(stream: MediaStream | null, active: boolean): number[] {
  const [levels, setLevels] = useState<number[]>(() => Array(LEVEL_BARS).fill(0));
  useEffect(() => {
    if (!stream || !active || stream.getAudioTracks().length === 0) return;
    const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
    if (!Ctx) return;
    const ctx = new Ctx();
    const source = ctx.createMediaStreamSource(stream);
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 512;
    source.connect(analyser);
    const samples = new Float32Array(analyser.fftSize);
    const timer = window.setInterval(() => {
      analyser.getFloatTimeDomainData(samples);
      let sum = 0;
      for (const v of samples) sum += v * v;
      // Speech sits around 0.02 to 0.2 RMS; this spreads it over the bar.
      const level = Math.min(1, Math.sqrt(sum / samples.length) * 6);
      setLevels((prev) => [...prev.slice(1), level]);
    }, 90);
    return () => {
      window.clearInterval(timer);
      source.disconnect();
      void ctx.close();
    };
  }, [stream, active]);
  return levels;
}

function LevelBars({ levels, className }: { levels: number[]; className?: string }) {
  return (
    // Newest on the right, as it is heard; a narrow bar keeps the oldest.
    <span className={cn('flex h-6 items-center justify-end gap-[3px] overflow-hidden', className)} aria-hidden="true" data-testid="recording-level">
      {levels.map((l, i) => (
        <span
          key={i}
          className="w-[3px] shrink-0 rounded-full bg-primary transition-[height] duration-75"
          style={{ height: `${Math.max(3, Math.round(l * 24))}px`, opacity: 0.4 + 0.6 * Math.min(1, l * 1.5) }}
        />
      ))}
    </span>
  );
}

/**
 * A voice message being recorded, in place of the message box. It starts
 * straight away; stopping attaches it, ready to send, and the bin throws it away.
 */
export function VoiceRecorder({ onDone, onCancel }: { onDone: (file: File) => void; onCancel: (problem?: string) => void }) {
  const stream = useRef<MediaStream | null>(null);
  const [live, setLive] = useState<MediaStream | null>(null);
  const { start, stop, finished, discard, seconds, recording } = useRecording('audio', MAX_VOICE_SECONDS);
  const [saving, setSaving] = useState(false);
  const levels = useLevels(live, recording);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const s = await navigator.mediaDevices.getUserMedia({ audio: true });
        if (cancelled) return stopTracks(s);
        stream.current = s;
        setLive(s);
        start(s);
        // Reaching the limit attaches what was recorded, as stopping would.
        const atLimit = await finished();
        if (atLimit && !cancelled) {
          stopTracks(stream.current);
          onDone(atLimit);
        }
      } catch (err) {
        if (!cancelled) onCancel(microphoneProblem(err));
      }
    })();
    return () => {
      cancelled = true;
      discard();
      stopTracks(stream.current);
    };
    // Once, when it appears.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const left = MAX_VOICE_SECONDS - seconds;
  return (
    <div
      className="flex min-w-0 flex-1 items-center gap-2 rounded-full border border-primary/30 bg-primary/5 py-1 pl-1 pr-1 sm:gap-3"
      role="group"
      aria-label="Recording a voice message"
    >
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="h-8 w-8 shrink-0 rounded-full text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
        aria-label="Discard the recording"
        title="Discard"
        onClick={() => onCancel()}
      >
        <Trash2 className="h-4 w-4" />
      </Button>
      <span className="flex shrink-0 items-center gap-1.5">
        <span className={cn('h-2 w-2 rounded-full bg-red-500', recording && 'animate-pulse')} aria-hidden="true" />
        <span className="text-sm font-medium tabular-nums" aria-live="off" data-testid="recording-time">
          {clock(seconds)}
        </span>
      </span>
      {recording ? (
        <LevelBars levels={levels} className="min-w-0 flex-1" />
      ) : (
        <span className="flex min-w-0 flex-1 items-center gap-2 truncate text-xs text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin" aria-hidden="true" />
          Starting the microphone…
        </span>
      )}
      {recording && left <= 30 && (
        <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">{Math.ceil(left)}s left</span>
      )}
      <Button
        type="button"
        size="sm"
        className="h-8 shrink-0 rounded-full px-3"
        disabled={!recording || saving}
        title={`Up to ${MAX_VOICE_SECONDS / 60} minutes`}
        onClick={async () => {
          setSaving(true);
          const file = await stop();
          stopTracks(stream.current);
          if (file) onDone(file);
          else onCancel('Nothing was recorded.');
        }}
      >
        {saving ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : <Check className="mr-1 h-4 w-4" />}
        Done
      </Button>
    </div>
  );
}

function microphoneProblem(err: unknown): string {
  const name = (err as { name?: string })?.name;
  if (name === 'NotAllowedError') return 'TeamUp is not allowed to use your microphone. Allow it in the browser and try again.';
  if (name === 'NotFoundError') return 'No microphone was found.';
  return (err as Error)?.message || 'Could not start recording.';
}

/**
 * A short video message, recorded with a preview of yourself: record, watch it
 * back, then attach it or try again.
 */
export function VideoNoteDialog({ open, onDone, onClose }: { open: boolean; onDone: (file: File) => void; onClose: () => void }) {
  const live = useRef<HTMLVideoElement>(null);
  const stream = useRef<MediaStream | null>(null);
  const { start, stop, finished, discard, seconds, recording } = useRecording('video', MAX_VIDEO_SECONDS);
  const [taken, setTaken] = useState<File | null>(null);
  const [preview, setPreview] = useState('');
  const [problem, setProblem] = useState('');
  // The camera's first frame is showing, so there is something to record.
  const [cameraReady, setCameraReady] = useState(false);

  const close = useCallback(() => {
    discard();
    stopTracks(stream.current);
    stream.current = null;
    setTaken(null);
    setProblem('');
    setCameraReady(false);
    onClose();
  }, [discard, onClose]);

  // The camera is on only while the dialog is open and nothing has been taken.
  useEffect(() => {
    if (!open || taken) return;
    let cancelled = false;
    setCameraReady(false);
    (async () => {
      try {
        const s = await navigator.mediaDevices.getUserMedia({
          audio: true,
          video: { width: { ideal: 640 }, height: { ideal: 640 }, facingMode: 'user' },
        });
        if (cancelled) return stopTracks(s);
        stream.current = s;
        if (live.current) live.current.srcObject = s;
      } catch (err) {
        if (!cancelled) setProblem(microphoneProblem(err).replace('microphone', 'camera and microphone'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, taken]);

  useEffect(() => {
    if (!taken) return;
    const url = URL.createObjectURL(taken);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [taken]);

  const record = async () => {
    if (!stream.current) return;
    try {
      start(stream.current);
    } catch (err) {
      setProblem((err as Error).message);
      return;
    }
    const atLimit = await finished();
    if (atLimit) {
      stopTracks(stream.current);
      stream.current = null;
      setTaken(atLimit);
    }
  };

  const finish = async () => {
    const file = await stop();
    stopTracks(stream.current);
    stream.current = null;
    if (file) setTaken(file);
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Video message</DialogTitle>
          <DialogDescription>Up to {MAX_VIDEO_SECONDS} seconds. You can watch it before sending.</DialogDescription>
        </DialogHeader>
        <div className="relative aspect-square overflow-hidden rounded-2xl bg-muted ring-1 ring-border">
          {taken ? (
            <VideoNotePlayer
              src={preview}
              filename="Your video message"
              testId="video-note-preview"
              className="h-full w-full rounded-none border-0 shadow-none"
            />
          ) : (
            <>
              <video
                ref={live}
                autoPlay
                muted
                playsInline
                onLoadedData={() => setCameraReady(true)}
                className={cn(
                  'h-full w-full -scale-x-100 object-cover transition-opacity duration-300',
                  cameraReady ? 'opacity-100' : 'opacity-0'
                )}
              />
              {!cameraReady && (
                <span className="absolute inset-0 flex flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted-foreground">
                  {problem ? (
                    <>
                      <Camera className="h-8 w-8" aria-hidden="true" />
                      <span className="text-destructive" role="alert">
                        {problem}
                      </span>
                    </>
                  ) : (
                    <>
                      <span className="relative flex h-12 w-12 items-center justify-center">
                        <span className="absolute inset-0 animate-ping rounded-full bg-primary/20" />
                        <Camera className="relative h-6 w-6 text-primary" aria-hidden="true" />
                      </span>
                      Starting the camera…
                    </>
                  )}
                </span>
              )}
            </>
          )}
          {recording && (
            <>
              <span className="absolute left-2 top-2 flex items-center gap-1.5 rounded-full bg-black/50 px-2.5 py-1 text-xs font-medium tabular-nums text-white backdrop-blur-sm">
                <span className="h-2 w-2 animate-pulse rounded-full bg-red-500" aria-hidden="true" />
                {clock(seconds)} / {clock(MAX_VIDEO_SECONDS)}
              </span>
              <span className="absolute inset-x-0 bottom-0 h-1 bg-black/30" aria-hidden="true">
                <span
                  className="block h-full bg-primary transition-[width] duration-200 ease-linear"
                  style={{ width: `${Math.min(100, (seconds / MAX_VIDEO_SECONDS) * 100)}%` }}
                />
              </span>
            </>
          )}
        </div>
        {problem && taken && (
          <p className="text-sm text-destructive" role="alert">
            {problem}
          </p>
        )}

        {taken ? (
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setTaken(null)}>
              <RotateCcw className="mr-2 h-4 w-4" />
              Again
            </Button>
            <Button
              onClick={() => {
                const file = taken;
                close();
                onDone(file);
              }}
            >
              <Check className="mr-2 h-4 w-4" />
              Attach
            </Button>
          </div>
        ) : (
          // A camera's shutter: a ring in the organization's colour around a
          // red dot that becomes a square while recording.
          <div className="grid grid-cols-[1fr_auto_1fr] items-center">
            <span>
              {!recording && (
                <Button variant="ghost" size="sm" onClick={close}>
                  <X className="mr-1 h-4 w-4" />
                  Cancel
                </Button>
              )}
            </span>
            <button
              type="button"
              onClick={recording ? finish : record}
              disabled={!recording && (!cameraReady || !!problem)}
              aria-label={recording ? 'Stop' : 'Record'}
              title={recording ? 'Stop recording' : 'Start recording'}
              className="flex h-16 w-16 items-center justify-center rounded-full border-4 border-primary/40 bg-background transition-colors hover:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <span
                className={cn(
                  'bg-red-500 transition-all duration-200',
                  recording ? 'h-6 w-6 rounded-md' : 'h-11 w-11 rounded-full'
                )}
              />
            </button>
            <span className="text-right text-xs text-muted-foreground">{recording ? 'Tap to stop' : ''}</span>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
