import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { createLocalVideoTrack, type LocalVideoTrack } from 'livekit-client';
import { AlertTriangle, CalendarClock, Loader2, Mic, MicOff, Users, Video, VideoOff, Volume2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { UserAvatar, usePictures } from '@/shared/components/UserAvatar';
import { useAuth } from '@/features/auth/AuthProvider';
import { JOIN_EARLY_MINUTES } from '@/features/calendar/layout';
import { shortcutHint } from '@/shared/shortcuts/catalogue';
import { useShortcut } from '@/shared/shortcuts/ShortcutsProvider';
import { formatDate, formatTime, cn } from '@/shared/utils/cn';
import { useActiveCall } from './ActiveCallProvider';
import { BackgroundPicker, previewButtonClass, useCameraEffects } from './BackgroundPicker';
import type { LobbyKind } from './lobby';
import {
  deviceLabel,
  deviceProblem,
  facesAndRest,
  presentPeople,
  problemText,
  readJoinChoice,
  saveJoinChoice,
  whoIsHere,
  type CallPerson,
  type DeviceProblem,
  type JoinChoice,
} from './prejoin';

/** What the screen shows of the call it is in front of. */
export interface PreJoinCall {
  title?: string;
  status?: string;
  scheduledStart?: string;
  standingMeetingId?: string;
  participants?: CallPerson[];
  /** Whether the details include who is on the call; a limited view does not. */
  canReadLog?: boolean;
}

/** The sound "Test speaker" plays: short, and already on every install. */
const TEST_SOUND = '/sounds/notification.mp3';

/** Whether this browser can send a page's sound to a chosen speaker. Safari and Firefox before 116 cannot. */
const canPickSpeaker = () => typeof HTMLMediaElement !== 'undefined' && 'setSinkId' in HTMLMediaElement.prototype;

/**
 * The screen before a video call or a meeting, as Google Meet has: your camera as
 * others will see it, background included, your microphone's level, the devices,
 * and who is already in. Nothing here asks for a token, because asking is what
 * opens a scheduled meeting (lobby.ts); only Join does.
 *
 * It also takes over from the old lobby of a meeting nobody has started: its
 * words stay, and somebody knocking can still be let in from here (watchDoor).
 */
export function PreJoinScreen({
  callId,
  call,
  lobby,
  onJoin,
}: {
  callId: string;
  call: PreJoinCall | undefined;
  /** Whether joining would start a scheduled meeting, and whether early. */
  lobby: Exclude<LobbyKind, 'missed'>;
  onJoin: (choice: JoinChoice) => void;
}) {
  const navigate = useNavigate();
  const { user } = useAuth();
  const userId = user?.id;
  const { activeCall, watchDoor } = useActiveCall();

  // Somebody at the door of a meeting nobody is in yet can be let in from here.
  useEffect(() => {
    watchDoor(callId);
    return () => watchDoor(null);
  }, [callId, watchDoor]);

  const [choice, setChoice] = useState<JoinChoice>(() => readJoinChoice(userId));
  const change = useCallback(
    (next: Partial<JoinChoice>) => {
      setChoice((current) => ({ ...current, ...next }));
      // Devices are remembered as soon as they are picked; on and off when you join.
      const devices = { audioInput: next.audioInput, videoInput: next.videoInput, audioOutput: next.audioOutput };
      const picked = Object.fromEntries(Object.entries(devices).filter(([, v]) => v !== undefined));
      if (Object.keys(picked).length > 0) saveJoinChoice(userId, picked);
    },
    [userId]
  );

  const [camProblem, setCamProblem] = useState<DeviceProblem | null>(null);
  const [micProblem, setMicProblem] = useState<DeviceProblem | null>(null);
  useBlockedPermissions(setCamProblem, setMicProblem);

  const { devices, known, refresh } = useDevices();
  const [releasing, setReleasing] = useState(false);

  const camera = useCameraPreview(choice.camera && !releasing, choice.videoInput, (problem) => {
    setCamProblem(problem);
    setChoice((c) => ({ ...c, camera: false }));
  });
  const mic = useMicrophone(choice.mic && !releasing, choice.audioInput, (problem) => {
    setMicProblem(problem);
    setChoice((c) => ({ ...c, mic: false }));
  });
  // Once a device is open the browser tells its name, and the others'.
  useEffect(() => {
    if (camera.track || mic.stream) void refresh();
  }, [camera.track, mic.stream, refresh]);
  useCameraEffects(camera.track);

  const cameras = devices.filter((d) => d.kind === 'videoinput');
  const microphones = devices.filter((d) => d.kind === 'audioinput');
  const noCamera = known && cameras.length === 0;
  const noMicrophone = known && microphones.length === 0;
  const camNote = camProblem ?? (noCamera ? 'missing' : null);
  const micNote = micProblem ?? (noMicrophone ? 'missing' : null);

  const toggleCamera = useCallback(() => {
    setCamProblem(null);
    setChoice((c) => ({ ...c, camera: !c.camera }));
  }, []);
  const toggleMic = useCallback(() => {
    setMicProblem(null);
    setChoice((c) => ({ ...c, mic: !c.mic }));
  }, []);
  // The call's own keys, here too. Ahead of a call you may already be in.
  useShortcut('call.mic', toggleMic, { priority: 1 });
  useShortcut('call.camera', toggleCamera, { priority: 1 });

  const join = () => {
    // The camera and microphone are let go before the call opens them again, so
    // neither is held twice and no light stays on.
    camera.stop();
    mic.stop();
    setReleasing(true);
    saveJoinChoice(userId, { mic: choice.mic, camera: choice.camera });
    onJoin(choice);
  };

  // Who came to a call is what its log's rule protects (callAsSeenBy), so somebody
  // who may join a standing meeting but not read its log is not told who is in.
  const knowsWho = call?.canReadLog === true;
  const { others, me } = presentPeople(call?.participants, userId);
  const started = call?.status === 'active';
  const start = call?.scheduledStart;
  const title = call?.title?.trim() || (start ? 'Scheduled meeting' : 'Video call');
  const elsewhere = !!activeCall && activeCall.callId !== callId;

  const micLabel = choice.mic ? 'Turn your microphone off' : 'Turn your microphone on';
  const camLabel = choice.camera ? 'Turn your camera off' : 'Turn your camera on';

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto grid w-full max-w-6xl gap-x-8 gap-y-5 px-4 py-5 sm:px-6 sm:py-8 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)] lg:items-start lg:py-12">
        {/* Your camera, as the others will see it. */}
        <section aria-label="Your camera" className="min-w-0 lg:col-start-1 lg:row-start-1">
          <div className="relative aspect-video w-full overflow-hidden rounded-2xl bg-slate-900 shadow-sm ring-1 ring-border">
            {camera.track ? (
              <PreviewVideo track={camera.track} />
            ) : (
              <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-4 text-center text-slate-200">
                {choice.camera ? (
                  <>
                    <Loader2 className="h-7 w-7 animate-spin" aria-hidden="true" />
                    <p className="text-sm">Starting your camera…</p>
                  </>
                ) : (
                  <>
                    <UserAvatar
                      name={user?.name}
                      src={user?.avatarUrl}
                      className="h-16 w-16 sm:h-20 sm:w-20"
                      fallbackClassName="bg-slate-700 text-lg text-white sm:text-xl"
                      decorative
                    />
                    <p className="text-sm">Your camera is off</p>
                  </>
                )}
              </div>
            )}
            <div className="absolute inset-x-0 bottom-0 flex items-center justify-center gap-3 bg-gradient-to-t from-black/60 to-transparent px-3 pb-3 pt-8">
              <button
                type="button"
                onClick={toggleMic}
                aria-label={micLabel}
                aria-pressed={choice.mic}
                {...shortcutHint('call.mic', micLabel)}
                className={previewButtonClass(false, !choice.mic)}
              >
                {choice.mic ? <Mic className="h-5 w-5" aria-hidden="true" /> : <MicOff className="h-5 w-5" aria-hidden="true" />}
              </button>
              <button
                type="button"
                onClick={toggleCamera}
                aria-label={camLabel}
                aria-pressed={choice.camera}
                {...shortcutHint('call.camera', camLabel)}
                className={previewButtonClass(false, !choice.camera)}
              >
                {choice.camera ? <Video className="h-5 w-5" aria-hidden="true" /> : <VideoOff className="h-5 w-5" aria-hidden="true" />}
              </button>
              <BackgroundPicker variant="preview" />
            </div>
          </div>
          {(camNote || micNote) && (
            <div className="mt-3 space-y-2">
              {camNote && <Problem text={problemText(camNote, 'camera')} />}
              {micNote && <Problem text={problemText(micNote, 'microphone')} />}
            </div>
          )}
        </section>

        {/* The call, who is in it, and the way in. */}
        <section aria-label="Join the call" className="min-w-0 lg:col-start-2 lg:row-start-1 lg:self-center">
          <h1 className="text-2xl font-semibold tracking-tight">Ready to join?</h1>
          <h2 className="mt-1 break-anywhere text-base font-medium">{title}</h2>
          {start && (
            <p className="mt-0.5 flex items-center gap-1.5 text-sm tabular-nums text-muted-foreground">
              <CalendarClock className="h-4 w-4 shrink-0" aria-hidden="true" />
              {formatDate(start)} at {formatTime(start)}
            </p>
          )}

          {knowsWho && <WhoIsHere others={others} me={me} />}

          {lobby !== 'none' && (
            <p className="mt-4 rounded-lg border border-border bg-muted/50 px-3 py-2 text-sm text-muted-foreground">
              {started
                ? 'Somebody has started it.'
                : lobby === 'early'
                  ? `It opens ${JOIN_EARLY_MINUTES} minutes before it starts. Joining now starts it for everybody, and it can no longer be moved.`
                  : 'Nobody has started it yet. Joining starts it for everybody.'}
            </p>
          )}
          {elsewhere && <p className="mt-3 text-sm text-muted-foreground">Joining leaves the call you are in now.</p>}

          <div className="mt-6 flex flex-wrap gap-2">
            <Button size="lg" className="min-w-32 flex-1 px-6 sm:flex-none" onClick={join} disabled={releasing}>
              {releasing ? (
                <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />
              ) : (
                <Video className="mr-2 h-4 w-4" aria-hidden="true" />
              )}
              {lobby === 'early' && !started ? 'Join now' : 'Join'}
            </Button>
            <Button size="lg" variant="outline" className="px-6" onClick={() => navigate('/calls')}>
              Back
            </Button>
          </div>
        </section>

        {/* Which devices. */}
        <section aria-label="Your devices" className="grid min-w-0 gap-4 sm:grid-cols-2 lg:col-start-1 lg:row-start-2">
          <div className="min-w-0">
            <DevicePicker
              kind="audioinput"
              label="Microphone"
              devices={microphones}
              value={choice.audioInput}
              onChange={(id) => change({ audioInput: id })}
            />
            <MicMeter stream={mic.stream} />
          </div>
          <DevicePicker
            kind="videoinput"
            label="Camera"
            devices={cameras}
            value={choice.videoInput}
            onChange={(id) => change({ videoInput: id })}
          />
          {canPickSpeaker() && (
            <div className="min-w-0 sm:col-span-2">
              <SpeakerPicker
                devices={devices.filter((d) => d.kind === 'audiooutput')}
                value={choice.audioOutput}
                onChange={(id) => change({ audioOutput: id })}
              />
            </div>
          )}
          {!canPickSpeaker() && (
            <div className="min-w-0 sm:col-span-2">
              <SpeakerTest output={undefined} />
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

/** Faces and names of whoever is in the call already, looked at again while the screen is open. */
function WhoIsHere({ others, me }: { others: CallPerson[]; me: boolean }) {
  const pictures = usePictures();
  const { faces, rest } = facesAndRest(others);
  return (
    <div className="mt-5 flex min-h-10 items-center gap-3" role="status" aria-live="polite" aria-label="Who is here">
      {faces.length === 0 ? (
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground" aria-hidden="true">
          <Users className="h-4 w-4" />
        </span>
      ) : (
        <div className="flex shrink-0 -space-x-2" aria-hidden="true">
          {faces.map((p) => (
            <UserAvatar key={p.userId} name={p.userName} src={pictures(p.userId)} className="h-9 w-9 ring-2 ring-background" decorative />
          ))}
          {rest > 0 && (
            <span className="flex h-9 min-w-9 items-center justify-center rounded-full bg-muted px-1.5 text-xs font-medium text-muted-foreground ring-2 ring-background">
              +{rest}
            </span>
          )}
        </div>
      )}
      <p className="min-w-0 text-sm">
        <span className="font-medium">{whoIsHere(others.map((p) => p.userName))}</span>
        {me && <span className="block text-muted-foreground">You are in it on another device too.</span>}
      </p>
    </div>
  );
}

function Problem({ text }: { text: string }) {
  return (
    <p role="alert" className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-foreground">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" aria-hidden="true" />
      <span>{text}</span>
    </p>
  );
}

/** Your camera, mirrored as it is to you in the call, so moving left moves left. */
function PreviewVideo({ track }: { track: LocalVideoTrack }) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    track.attach(element);
    return () => {
      track.detach(element);
    };
  }, [track]);
  return (
    <video
      ref={ref}
      autoPlay
      playsInline
      muted
      data-testid="prejoin-preview"
      className="absolute inset-0 h-full w-full -scale-x-100 object-cover"
    />
  );
}

/** Devices the browser lists; their names come only once one of them is open. */
function useDevices() {
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);
  const [known, setKnown] = useState(false);
  const refresh = useCallback(async () => {
    try {
      const list = await navigator.mediaDevices.enumerateDevices();
      setDevices(list);
      setKnown(true);
    } catch {
      // No list on this browser: the defaults are used.
    }
  }, []);
  useEffect(() => {
    void refresh();
    const media = navigator.mediaDevices;
    media?.addEventListener?.('devicechange', refresh);
    return () => media?.removeEventListener?.('devicechange', refresh);
  }, [refresh]);
  return { devices, known, refresh };
}

/**
 * Says at once that the browser has been told not to share the camera or
 * microphone, where it can tell, rather than only after a button does nothing.
 */
function useBlockedPermissions(onCamera: (p: DeviceProblem) => void, onMic: (p: DeviceProblem) => void) {
  useEffect(() => {
    let live = true;
    const check = (name: string, report: (p: DeviceProblem) => void) => {
      navigator.permissions
        ?.query({ name: name as PermissionName })
        .then((status) => {
          if (live && status.state === 'denied') report('denied');
        })
        .catch(() => {
          // This browser does not say; trying the device will.
        });
    };
    check('camera', onCamera);
    check('microphone', onMic);
    return () => {
      live = false;
    };
  }, [onCamera, onMic]);
}

/** The camera's picture while it is on here, through LiveKit so the background can go on it. */
function useCameraPreview(on: boolean, deviceId: string | undefined, onProblem: (p: DeviceProblem) => void) {
  const [track, setTrack] = useState<LocalVideoTrack | undefined>();
  const current = useRef<LocalVideoTrack | undefined>();
  const problemRef = useRef(onProblem);
  problemRef.current = onProblem;

  useEffect(() => {
    if (!on) return;
    let live = true;
    createLocalVideoTrack({ deviceId, facingMode: 'user', resolution: { width: 1280, height: 720 } })
      .then((made) => {
        if (!live) {
          made.stop();
          return;
        }
        current.current = made;
        setTrack(made);
      })
      .catch((err) => {
        if (live) problemRef.current(deviceProblem(err));
      });
    return () => {
      live = false;
      current.current?.stop();
      current.current = undefined;
      setTrack(undefined);
    };
  }, [on, deviceId]);

  const stop = useCallback(() => {
    current.current?.stop();
  }, []);
  return { track, stop };
}

/** The microphone while it is on here, for its level; nothing is sent anywhere. */
function useMicrophone(on: boolean, deviceId: string | undefined, onProblem: (p: DeviceProblem) => void) {
  const [stream, setStream] = useState<MediaStream | undefined>();
  const current = useRef<MediaStream | undefined>();
  const problemRef = useRef(onProblem);
  problemRef.current = onProblem;

  useEffect(() => {
    if (!on) return;
    let live = true;
    const stopAll = (s: MediaStream | undefined) => s?.getTracks().forEach((t) => t.stop());
    navigator.mediaDevices
      .getUserMedia({ audio: deviceId ? { deviceId } : true })
      .then((made) => {
        if (!live) {
          stopAll(made);
          return;
        }
        current.current = made;
        setStream(made);
      })
      .catch((err) => {
        if (live) problemRef.current(deviceProblem(err));
      });
    return () => {
      live = false;
      stopAll(current.current);
      current.current = undefined;
      setStream(undefined);
    };
  }, [on, deviceId]);

  const stop = useCallback(() => {
    current.current?.getTracks().forEach((t) => t.stop());
  }, []);
  return { stream, stop };
}

/**
 * How loud your microphone is, live: say something and the bar moves. Drawn from
 * its own animation frames, so the rest of the screen is not redrawn for it.
 */
function MicMeter({ stream }: { stream: MediaStream | undefined }) {
  const bar = useRef<HTMLDivElement>(null);
  const meter = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const fill = bar.current;
    const box = meter.current;
    if (!stream || !fill || !box) return;
    const Ctor = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!Ctor) return;
    let ctx: AudioContext;
    try {
      ctx = new Ctor();
    } catch {
      return;
    }
    const source = ctx.createMediaStreamSource(stream);
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 512;
    source.connect(analyser);
    const samples = new Uint8Array(analyser.fftSize);
    let frame = 0;
    let shown = 0;
    let lastSaid = 0;
    const draw = (time: number) => {
      analyser.getByteTimeDomainData(samples);
      let sum = 0;
      for (const s of samples) {
        const v = (s - 128) / 128;
        sum += v * v;
      }
      // Speech sits low on a straight scale; this spreads it across the bar.
      const level = Math.min(1, Math.sqrt(sum / samples.length) * 4);
      shown = Math.max(level, shown * 0.85);
      const percent = Math.round(shown * 100);
      fill.style.transform = `scaleX(${shown})`;
      if (time - lastSaid > 250) {
        lastSaid = time;
        box.setAttribute('aria-valuenow', String(percent));
        box.dataset.level = String(percent);
      }
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);
    // A page nobody has clicked yet may start it suspended.
    void ctx.resume().catch(() => undefined);
    return () => {
      cancelAnimationFrame(frame);
      source.disconnect();
      void ctx.close().catch(() => undefined);
      fill.style.transform = 'scaleX(0)';
      box.setAttribute('aria-valuenow', '0');
      box.dataset.level = '0';
    };
  }, [stream]);

  return (
    <div className="mt-2 flex items-center gap-2">
      <Mic className={cn('h-3.5 w-3.5 shrink-0', stream ? 'text-foreground' : 'text-muted-foreground')} aria-hidden="true" />
      <div
        ref={meter}
        role="meter"
        aria-label="Microphone level"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={0}
        data-level="0"
        data-testid="mic-level"
        className="relative h-1.5 flex-1 overflow-hidden rounded-full bg-muted"
      >
        <div ref={bar} className="absolute inset-0 origin-left scale-x-0 rounded-full bg-primary" />
      </div>
      <span className="w-24 shrink-0 text-right text-xs text-muted-foreground">{stream ? 'Say something' : 'Microphone off'}</span>
    </div>
  );
}

function DevicePicker({
  kind,
  label,
  devices,
  value,
  onChange,
}: {
  kind: MediaDeviceKind;
  label: string;
  devices: MediaDeviceInfo[];
  value: string | undefined;
  onChange: (deviceId: string) => void;
}) {
  // Before access is given a browser lists one nameless entry with no id, which
  // cannot be chosen; the default is used until then.
  const choosable = useMemo(() => devices.filter((d) => d.deviceId), [devices]);
  const selected = value && choosable.some((d) => d.deviceId === value) ? value : choosable[0]?.deviceId;
  const id = `prejoin-${kind}`;
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      <Select value={selected} onValueChange={onChange} disabled={choosable.length === 0}>
        <SelectTrigger id={id} aria-label={`Choose a ${label.toLowerCase()}`} className="min-w-0">
          <SelectValue placeholder={choosable.length === 0 ? 'Default' : `Choose a ${label.toLowerCase()}`} />
        </SelectTrigger>
        <SelectContent>
          {choosable.map((d, i) => (
            <SelectItem key={d.deviceId} value={d.deviceId}>
              {deviceLabel(d, i, kind)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function SpeakerPicker({
  devices,
  value,
  onChange,
}: {
  devices: MediaDeviceInfo[];
  value: string | undefined;
  onChange: (deviceId: string) => void;
}) {
  const choosable = devices.filter((d) => d.deviceId);
  const selected = value && choosable.some((d) => d.deviceId === value) ? value : choosable[0]?.deviceId;
  return (
    <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-end">
      <div className="min-w-0 flex-1">
        <DevicePicker kind="audiooutput" label="Speaker" devices={devices} value={value} onChange={onChange} />
      </div>
      <SpeakerTest output={selected} />
    </div>
  );
}

/** Plays a short sound through the chosen speaker, to hear that it is the right one. */
function SpeakerTest({ output }: { output: string | undefined }) {
  const [state, setState] = useState<'idle' | 'playing' | 'failed'>('idle');
  const audio = useRef<HTMLAudioElement | null>(null);
  useEffect(() => () => audio.current?.pause(), []);

  const play = async () => {
    audio.current?.pause();
    const sound = new Audio(TEST_SOUND);
    audio.current = sound;
    try {
      const sink = sound as HTMLAudioElement & { setSinkId?: (id: string) => Promise<void> };
      if (output && sink.setSinkId) await sink.setSinkId(output);
      sound.onended = () => setState('idle');
      setState('playing');
      await sound.play();
    } catch {
      setState('failed');
    }
  };

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
      <Button variant="outline" className="shrink-0" onClick={() => void play()} disabled={state === 'playing'}>
        <Volume2 className="mr-2 h-4 w-4" aria-hidden="true" />
        {state === 'playing' ? 'Playing…' : 'Test speaker'}
      </Button>
      {state === 'failed' && (
        <span role="alert" className="text-sm text-muted-foreground">
          The sound could not play. Check your speaker and its volume.
        </span>
      )}
    </div>
  );
}
