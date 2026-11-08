import { useCallback, useEffect, useRef, useState } from 'react';
import { Check, Circle, Loader2, RotateCcw, Square, Trash2, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { cn } from '@/shared/utils/cn';

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

/**
 * A voice message being recorded, in place of the message box. It starts
 * straight away; stopping attaches it, ready to send, and the bin throws it away.
 */
export function VoiceRecorder({ onDone, onCancel }: { onDone: (file: File) => void; onCancel: (problem?: string) => void }) {
  const stream = useRef<MediaStream | null>(null);
  const { start, stop, finished, discard, seconds, recording } = useRecording('audio', MAX_VOICE_SECONDS);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const s = await navigator.mediaDevices.getUserMedia({ audio: true });
        if (cancelled) return stopTracks(s);
        stream.current = s;
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

  return (
    <div className="flex flex-1 items-center gap-3 rounded-md border border-input bg-background px-3 py-1.5" role="group" aria-label="Recording a voice message">
      <span className={cn('h-2.5 w-2.5 shrink-0 rounded-full bg-red-500', recording && 'animate-pulse')} aria-hidden="true" />
      <span className="text-sm tabular-nums" aria-live="off" data-testid="recording-time">
        {clock(seconds)}
      </span>
      <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
        {recording ? `Recording, up to ${MAX_VOICE_SECONDS / 60} minutes` : 'Starting the microphone…'}
      </span>
      <Button type="button" variant="ghost" size="icon" className="h-8 w-8" aria-label="Discard the recording" onClick={() => onCancel()}>
        <Trash2 className="h-4 w-4" />
      </Button>
      <Button
        type="button"
        size="sm"
        className="h-8"
        disabled={!recording || saving}
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

  const close = useCallback(() => {
    discard();
    stopTracks(stream.current);
    stream.current = null;
    setTaken(null);
    setProblem('');
    onClose();
  }, [discard, onClose]);

  // The camera is on only while the dialog is open and nothing has been taken.
  useEffect(() => {
    if (!open || taken) return;
    let cancelled = false;
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
        <div className="relative aspect-square overflow-hidden rounded-lg bg-black">
          {taken ? (
            <video src={preview} controls playsInline className="h-full w-full object-cover" data-testid="video-note-preview" />
          ) : (
            <video ref={live} autoPlay muted playsInline className="h-full w-full -scale-x-100 object-cover" />
          )}
          {recording && (
            <span className="absolute left-2 top-2 flex items-center gap-1.5 rounded bg-black/60 px-2 py-0.5 text-xs text-white">
              <span className="h-2 w-2 animate-pulse rounded-full bg-red-500" aria-hidden="true" />
              {clock(seconds)} / {clock(MAX_VIDEO_SECONDS)}
            </span>
          )}
        </div>
        {problem && (
          <p className="text-sm text-destructive" role="alert">
            {problem}
          </p>
        )}
        <div className="flex justify-end gap-2">
          {taken ? (
            <>
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
            </>
          ) : recording ? (
            <Button variant="destructive" onClick={finish}>
              <Square className="mr-2 h-4 w-4" />
              Stop
            </Button>
          ) : (
            <>
              <Button variant="outline" onClick={close}>
                <X className="mr-2 h-4 w-4" />
                Cancel
              </Button>
              <Button onClick={record} disabled={!!problem}>
                <Circle className="mr-2 h-4 w-4 fill-current text-red-500" />
                Record
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

