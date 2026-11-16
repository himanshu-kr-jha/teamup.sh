import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, Loader2, MapPin, RotateCcw, SwitchCamera, Trash2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Label } from '@/shared/components/ui/label';
import { Textarea } from '@/shared/components/ui/textarea';
import { fieldWorkApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { cn, formatTime } from '@/shared/utils/cn';
import { Link } from 'react-router-dom';
import { mapLink } from './AttendanceDetails';

/** The long side of the photo sent, which keeps it to a few hundred kilobytes. */
const PHOTO_EDGE = 1280;

/** "23.34410° N, 85.30960° E" */
export function coordinates(latitude: number, longitude: number): string {
  const lat = `${Math.abs(latitude).toFixed(5)}° ${latitude >= 0 ? 'N' : 'S'}`;
  const lng = `${Math.abs(longitude).toFixed(5)}° ${longitude >= 0 ? 'E' : 'W'}`;
  return `${lat}, ${lng}`;
}

/** An address in lines that fit across a photo, broken after its commas, at most two. */
export function addressLines(address: string, width = 64): string[] {
  const lines: string[] = [];
  let line = '';
  for (const part of address.split(',').map((p) => p.trim()).filter(Boolean)) {
    const next = line ? `${line}, ${part}` : part;
    if (next.length > width && line) {
      lines.push(`${line},`);
      line = part;
    } else line = next;
  }
  if (line) lines.push(line);
  if (lines.length > 2) return [lines[0], `${lines.slice(1).join(' ').slice(0, width - 1)}…`];
  return lines.map((l) => (l.length > width ? `${l.slice(0, width - 1)}…` : l));
}

/** What is printed on the photo: when, where and how closely, the address around it, and the note. */
export function stampLines(
  at: Date,
  place: { latitude: number; longitude: number; accuracyM: number },
  note: string,
  address = ''
): string[] {
  const when = at.toLocaleString('en-IN', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'short',
  });
  const lines = [when, `${coordinates(place.latitude, place.longitude)}  ±${Math.round(place.accuracyM)} m`, ...addressLines(address)];
  const trimmed = note.trim();
  if (trimmed) lines.push(trimmed.length > 70 ? `${trimmed.slice(0, 69)}…` : trimmed);
  return lines;
}

/**
 * The photo with the time and place printed across its foot, as a GPS camera
 * does, so it says where and when it was taken wherever it is looked at.
 */
function stampPhoto(raw: string, lines: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const canvas = document.createElement('canvas');
      canvas.width = img.naturalWidth;
      canvas.height = img.naturalHeight;
      const ctx = canvas.getContext('2d');
      if (!ctx) return reject(new Error('no canvas'));
      ctx.drawImage(img, 0, 0);
      const size = Math.max(14, Math.round(canvas.width / 40));
      const pad = Math.round(size * 0.6);
      const band = lines.length * size * 1.35 + pad * 2;
      ctx.fillStyle = 'rgba(0, 0, 0, 0.6)';
      ctx.fillRect(0, canvas.height - band, canvas.width, band);
      ctx.fillStyle = '#ffffff';
      ctx.font = `600 ${size}px system-ui, -apple-system, Segoe UI, Roboto, sans-serif`;
      ctx.textBaseline = 'top';
      lines.forEach((line, i) => ctx.fillText(line, pad, canvas.height - band + pad + i * size * 1.35, canvas.width - pad * 2));
      resolve(canvas.toDataURL('image/jpeg', 0.85));
    };
    img.onerror = () => reject(new Error('the photo could not be read'));
    img.src = raw;
  });
}

/**
 * Working away today: a photo taken here and now, with where the phone is, so
 * the day's stand-ups count it as field work rather than an absence. Shown only
 * when the organization has turned field work on, to people a stand-up is for.
 */
export function FieldWorkBar() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const { data: settings } = useQuery({
    queryKey: ['field-work-settings'],
    queryFn: () => fieldWorkApi.settings().then((res) => res.data),
    staleTime: 60_000,
  });
  const { data: mine = [] } = useQuery({
    queryKey: ['field-work-mine'],
    queryFn: () => fieldWorkApi.mine().then((res) => res.data),
    enabled: !!settings?.enabled,
  });
  const remove = useMutation({
    mutationFn: (id: string) => fieldWorkApi.remove(id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['field-work-mine'] }),
    onError: (err) => toast.error(apiErrorMessage(err, 'Could not take it back.')),
  });
  if (!settings?.enabled) return null;
  const latest = mine[0];

  return (
    <div
      className="flex flex-col gap-2 rounded-lg border border-border bg-card px-3 py-2 text-sm sm:flex-row sm:items-center"
      data-testid="field-work-bar"
    >
      <MapPin className="hidden h-4 w-4 shrink-0 text-primary sm:block" aria-hidden="true" />
      {latest ? (
        <p className="min-w-0 flex-1">
          <span className="font-medium">In the field today.</span>{' '}
          <span className="text-muted-foreground">
            Checked in at {formatTime(latest.takenAt)}
            {latest.address ? ` near ${latest.address}` : ''}
            {latest.note ? `: ${latest.note}` : ''}.{' '}
            <a
              href={mapLink(latest.latitude, latest.longitude)}
              target="_blank"
              rel="noopener noreferrer"
              className="underline underline-offset-2 hover:text-foreground"
            >
              On the map
            </a>
          </span>
        </p>
      ) : (
        <p className="min-w-0 flex-1 text-muted-foreground">
          Working away from the office today? Check in from the field, and today&rsquo;s stand-ups count you as in the
          field, not absent.
        </p>
      )}
      <div className="flex shrink-0 flex-wrap items-center gap-2">
        <Link to="/field-work" className="text-sm text-muted-foreground underline underline-offset-2 hover:text-foreground">
          Your check-ins
        </Link>
        {latest && (
          <Button
            variant="ghost"
            size="sm"
            disabled={remove.isPending}
            onClick={() => remove.mutate(latest.id)}
            aria-label="Take back today's field work check-in"
          >
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </Button>
        )}
        <Button size="sm" variant={latest ? 'outline' : 'default'} onClick={() => setOpen(true)}>
          <Camera className="mr-1.5 h-4 w-4" aria-hidden="true" />
          {latest ? 'Check in again' : 'Check in from the field'}
        </Button>
      </div>
      {open && <CheckInDialog onClose={() => setOpen(false)} />}
    </div>
  );
}

type Place = { latitude: number; longitude: number; accuracyM: number };
type Facing = 'environment' | 'user';

/**
 * Checking in: the camera, the place and a note. On a phone it fills the
 * screen, as a camera app does, with the shutter at the foot and a switch to
 * the front camera for a photo of yourself at the site.
 */
function CheckInDialog({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const videoRef = useRef<HTMLVideoElement>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const [cameraProblem, setCameraProblem] = useState('');
  const [facing, setFacing] = useState<Facing>('environment');
  const [cameras, setCameras] = useState(0);
  // The picture as taken, and as sent: with the time, place and address printed on it.
  const [photo, setPhoto] = useState('');
  const [takenAt, setTakenAt] = useState<Date | null>(null);
  const [stamped, setStamped] = useState('');
  const [place, setPlace] = useState<Place | null>(null);
  const [placeProblem, setPlaceProblem] = useState('');
  const [note, setNote] = useState('');

  // The camera, live; the back one unless the front is asked for. Only a photo
  // taken now counts, so there is no file picker.
  useEffect(() => {
    let stopped = false;
    if (photo) return;
    if (!navigator.mediaDevices?.getUserMedia) {
      setCameraProblem('This browser cannot open the camera. Use the TeamUp app or Chrome on your phone.');
      return;
    }
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: { ideal: facing }, width: { ideal: 1920 } }, audio: false })
      .then((stream) => {
        if (stopped) {
          stream.getTracks().forEach((t) => t.stop());
          return;
        }
        streamRef.current = stream;
        if (videoRef.current) videoRef.current.srcObject = stream;
        setCameraProblem('');
        // Only once the camera is allowed does the browser say how many there are.
        navigator.mediaDevices
          .enumerateDevices()
          .then((all) => !stopped && setCameras(all.filter((d) => d.kind === 'videoinput').length))
          .catch(() => {});
      })
      .catch(() => setCameraProblem('The camera could not be opened. Allow TeamUp to use it, then try again.'));
    return () => {
      stopped = true;
      streamRef.current?.getTracks().forEach((t) => t.stop());
      streamRef.current = null;
    };
  }, [photo, facing]);

  // Where the phone is, as precisely as it can say, asked for at once.
  const locate = () => {
    setPlaceProblem('');
    if (!navigator.geolocation) {
      setPlaceProblem('This browser cannot tell where you are.');
      return;
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => setPlace({ latitude: pos.coords.latitude, longitude: pos.coords.longitude, accuracyM: pos.coords.accuracy }),
      () => setPlaceProblem('Your location could not be read. Allow TeamUp to use it, and turn location on.'),
      { enableHighAccuracy: true, timeout: 20_000, maximumAge: 0 }
    );
  };
  useEffect(locate, []);

  // The address around the place, which the server looks up and keeps with the check-in.
  const { data: address = '', isFetching: findingAddress } = useQuery({
    queryKey: ['field-work-place', place?.latitude.toFixed(4), place?.longitude.toFixed(4)],
    queryFn: () => fieldWorkApi.place(place!.latitude, place!.longitude),
    enabled: !!place,
    staleTime: Infinity,
    retry: false,
  });

  const take = () => {
    const video = videoRef.current;
    if (!video || !video.videoWidth) return;
    const scale = Math.min(1, PHOTO_EDGE / Math.max(video.videoWidth, video.videoHeight));
    const canvas = document.createElement('canvas');
    canvas.width = Math.round(video.videoWidth * scale);
    canvas.height = Math.round(video.videoHeight * scale);
    canvas.getContext('2d')?.drawImage(video, 0, 0, canvas.width, canvas.height);
    setPhoto(canvas.toDataURL('image/jpeg', 0.85));
    setTakenAt(new Date());
  };

  // Reprinted when the place or its address arrives or the note changes, so the preview is what is sent.
  useEffect(() => {
    if (!photo || !place || !takenAt || findingAddress) {
      setStamped('');
      return;
    }
    let live = true;
    stampPhoto(photo, stampLines(takenAt, place, note, address))
      .then((url) => live && setStamped(url))
      .catch(() => live && setStamped(''));
    return () => {
      live = false;
    };
  }, [photo, place, takenAt, note, address, findingAddress]);

  const send = useMutation({
    mutationFn: () => fieldWorkApi.checkIn({ photo: stamped, ...place!, note: note.trim() }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['field-work-mine'] });
      void queryClient.invalidateQueries({ queryKey: ['attendance'] });
      toast.success("Checked in. Today's stand-ups count you as in the field.");
      onClose();
    },
    onError: (err) => toast.error(apiErrorMessage(err, 'Could not send your check-in.')),
  });

  const front = facing === 'user';
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className={cn(
          'sm:max-w-md',
          // A phone: the whole screen, the camera taking all the room the rest leaves.
          'max-sm:inset-0 max-sm:left-0 max-sm:top-0 max-sm:flex max-sm:h-[100dvh] max-sm:max-h-none max-sm:max-w-none',
          'max-sm:translate-x-0 max-sm:translate-y-0 max-sm:flex-col max-sm:gap-0 max-sm:overflow-hidden max-sm:border-0 max-sm:p-0'
        )}
      >
        <DialogHeader className="max-sm:px-4 max-sm:py-3 max-sm:pr-12">
          <DialogTitle>Check in from the field</DialogTitle>
          <DialogDescription className="max-sm:sr-only">
            Take a photo of where you are working. It goes with your location now, and today&rsquo;s stand-ups count
            you as in the field. Your manager and HR can see the photo.
          </DialogDescription>
        </DialogHeader>

        <div className="relative overflow-hidden rounded-md bg-black max-sm:min-h-0 max-sm:flex-1 max-sm:rounded-none">
          {photo ? (
            <img
              src={stamped || photo}
              alt="The photo you took"
              className="aspect-[4/3] w-full object-contain max-sm:aspect-auto max-sm:h-full"
            />
          ) : cameraProblem ? (
            <p
              role="alert"
              className="flex aspect-[4/3] items-center justify-center p-4 text-center text-sm text-white/80 max-sm:aspect-auto max-sm:h-full"
            >
              {cameraProblem}
            </p>
          ) : (
            <video
              ref={videoRef}
              autoPlay
              playsInline
              muted
              // The front camera is shown as a mirror, as phones do; the photo is not.
              className={cn('aspect-[4/3] w-full object-cover max-sm:aspect-auto max-sm:h-full', front && '-scale-x-100')}
              aria-label={front ? 'Your front camera' : 'Your camera'}
            />
          )}

          {!photo && cameras > 1 && !cameraProblem && (
            <Button
              type="button"
              size="icon"
              variant="secondary"
              className="absolute right-3 top-3 h-11 w-11 rounded-full bg-black/50 text-white hover:bg-black/70"
              onClick={() => setFacing(front ? 'environment' : 'user')}
              aria-label={front ? 'Switch to the back camera' : 'Switch to the front camera'}
            >
              <SwitchCamera className="h-5 w-5" aria-hidden="true" />
            </Button>
          )}

          <div className="absolute inset-x-0 bottom-4 flex justify-center">
            {photo ? (
              <Button
                variant="secondary"
                className="rounded-full bg-black/60 text-white hover:bg-black/75"
                onClick={() => {
                  setPhoto('');
                  setStamped('');
                }}
              >
                <RotateCcw className="mr-1.5 h-4 w-4" aria-hidden="true" />
                Take another
              </Button>
            ) : (
              <button
                type="button"
                onClick={take}
                disabled={!!cameraProblem}
                aria-label="Take photo"
                className="flex h-16 w-16 items-center justify-center rounded-full border-4 border-white bg-white/25 transition active:scale-95 disabled:opacity-40"
              >
                <span className="h-11 w-11 rounded-full bg-white" />
              </button>
            )}
          </div>
        </div>

        <div className="space-y-3 max-sm:shrink-0 max-sm:px-4 max-sm:pb-[max(1rem,env(safe-area-inset-bottom))] max-sm:pt-3">
          <div className="space-y-0.5 text-sm" role="status">
            <p className="flex items-center gap-1.5">
              <MapPin className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              {place ? (
                <span className="tabular-nums">
                  {coordinates(place.latitude, place.longitude)}, within about {Math.round(place.accuracyM)} m
                </span>
              ) : placeProblem ? (
                <span className="text-muted-foreground">
                  {placeProblem}{' '}
                  <button type="button" className="underline underline-offset-2" onClick={locate}>
                    Try again
                  </button>
                </span>
              ) : (
                <span className="flex items-center gap-1.5 text-muted-foreground">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                  Finding where you are
                </span>
              )}
            </p>
            {place && (findingAddress || address) && (
              <p className="pl-[1.375rem] text-muted-foreground" data-testid="field-address">
                {findingAddress ? 'Finding the address…' : `Near ${address}`}
              </p>
            )}
          </div>

          <div className="space-y-1">
            <Label htmlFor="fieldNote">Note (optional)</Label>
            <Textarea
              id="fieldNote"
              rows={2}
              maxLength={500}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Survey at the Ranchi site, block C"
              className="max-sm:min-h-0"
            />
          </div>

          <div className="flex justify-end gap-2">
            <Button variant="outline" className="max-sm:flex-1" onClick={onClose}>
              Cancel
            </Button>
            <Button className="max-sm:flex-1" disabled={!stamped || !place || send.isPending} onClick={() => send.mutate()}>
              {send.isPending ? <Loader2 className="mr-1.5 h-4 w-4 animate-spin" aria-hidden="true" /> : null}
              Send check-in
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
