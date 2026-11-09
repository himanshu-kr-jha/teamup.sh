import { useCallback, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  LiveKitRoom,
  RoomAudioRenderer,
  useParticipants,
  useRoomContext,
} from "@livekit/components-react";
import { DisconnectReason } from "livekit-client";
import { format } from "date-fns";
import {
  CalendarClock,
  Loader2,
  LogOut,
  Mic,
  MicOff,
  Video,
  VideoOff,
} from "lucide-react";
import { Button } from "@/shared/components/ui/button";
import { Input } from "@/shared/components/ui/input";
import { Label } from "@/shared/components/ui/label";
import { BrandMark } from "@/features/branding/BrandMark";
import { guestApi, type GuestMeeting } from "@/shared/api/client";
import { apiErrorMessage } from "@/shared/api/errors";
import { cn } from "@/shared/utils/cn";
import { BlockedAudioPrompt, liveKitURL } from "./ActiveCallProvider";
import { CallStage } from "./CallStage";
import { CallControls, ShareUnavailableNote } from "./CallControls";
import { NotesBanner, isNotetaker } from "./notetaker";
import { CameraBackground } from './BackgroundPicker';

const NAME_KEY = "teamup.guestName";

function rememberedName(): string {
  try {
    return localStorage.getItem(NAME_KEY) ?? "";
  } catch {
    return "";
  }
}

/**
 * What lets this browser back into a meeting it has joined, without the
 * passcode: the key the server gave, and the name it was given under. Kept per
 * link, and forgotten when the guest is removed.
 */
type Saved = { key: string; name: string };
const savedKey = (token: string) => `teamup.guest.${token}`;

function savedJoin(token: string): Saved | null {
  try {
    const raw = localStorage.getItem(savedKey(token));
    const saved = raw ? (JSON.parse(raw) as Saved) : null;
    return saved?.key && saved.name ? saved : null;
  } catch {
    return null;
  }
}

function saveJoin(token: string, saved: Saved | null) {
  try {
    if (saved) localStorage.setItem(savedKey(token), JSON.stringify(saved));
    else localStorage.removeItem(savedKey(token));
  } catch {
    // Private mode: they type the passcode again next time.
  }
}

type Joined = { token: string; title: string; mic: boolean; camera: boolean };
type Left = { reason: "left" | "removed" | "ended" | "lost" };

/**
 * A meeting's guest page: somebody from outside the organization, with no
 * account, gives their name and the passcode and joins once the meeting is on.
 */
export function GuestJoinPage() {
  const { token = "" } = useParams();
  const [joined, setJoined] = useState<Joined | null>(null);
  const [left, setLeft] = useState<Left | null>(null);

  const {
    data: meeting,
    error,
    isPending,
  } = useQuery({
    queryKey: ["guest-meeting", token],
    queryFn: async () => (await guestApi.meeting(token)).data,
    retry: false,
    // Waiting for the meeting to start, the page lets them in when it does.
    refetchInterval: (q) =>
      !joined && q.state.data?.state === "waiting" ? 5_000 : false,
  });

  const onLeft = useCallback(
    (reason: Left["reason"]) => {
      // Removed means removed: this browser does not get back in on its own.
      if (reason === "removed") saveJoin(token, null);
      setJoined(null);
      setLeft({ reason });
    },
    [token],
  );

  let body: React.ReactNode;
  if (joined) {
    return <GuestRoom joined={joined} onLeft={onLeft} />;
  } else if (isPending) {
    body = (
      <Loader2
        className="mx-auto h-6 w-6 animate-spin text-muted-foreground"
        aria-label="Loading"
      />
    );
  } else if (error || !meeting) {
    body = (
      <Notice title="This link does not work">
        {apiErrorMessage(
          error,
          "The meeting may be over, or the organizer has turned its guest link off.",
        )}{" "}
        Ask whoever sent it for a new one.
      </Notice>
    );
  } else if (left) {
    body = (
      <AfterLeaving
        meeting={meeting}
        left={left}
        onRejoin={() => setLeft(null)}
      />
    );
  } else {
    body = (
      <JoinForm
        token={token}
        meeting={meeting}
        onJoined={(j) => {
          setLeft(null);
          setJoined(j);
        }}
      />
    );
  }

  return (
    <div className="flex min-h-viewport flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center border-b border-border px-4">
        <BrandMark />
      </header>
      <main className="flex flex-1 items-start justify-center px-4 py-8 sm:items-center">
        <div className="w-full max-w-sm">{body}</div>
      </main>
    </div>
  );
}

function Notice({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2 text-center" data-testid="guest-notice">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="text-sm text-muted-foreground">{children}</p>
    </div>
  );
}

function When({ meeting }: { meeting: GuestMeeting }) {
  if (!meeting.startsAt) return null;
  const start = new Date(meeting.startsAt);
  return (
    <p className="flex items-center justify-center gap-2 text-sm text-muted-foreground tabular-nums">
      <CalendarClock className="h-4 w-4 shrink-0" aria-hidden="true" />
      {format(start, "EEEE d MMMM, HH:mm")}
      {meeting.endsAt ? ` to ${format(new Date(meeting.endsAt), "HH:mm")}` : ""}
    </p>
  );
}

function JoinForm({
  token,
  meeting,
  onJoined,
}: {
  token: string;
  meeting: GuestMeeting;
  onJoined: (j: Joined) => void;
}) {
  const [saved, setSaved] = useState(() => savedJoin(token));
  const [name, setName] = useState(rememberedName);
  const [passcode, setPasscode] = useState("");
  const [mic, setMic] = useState(true);
  const [camera, setCamera] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");

  if (meeting.state === "ended") {
    return <Notice title={meeting.title}>This meeting has ended.</Notice>;
  }
  const waiting = meeting.state === "waiting";

  const join = async () => {
    setBusy(true);
    setProblem("");
    try {
      const res = saved
        ? await guestApi.join(token, { name: saved.name, rejoinKey: saved.key })
        : await guestApi.join(token, { name, passcode });
      try {
        localStorage.setItem(NAME_KEY, res.data.name);
      } catch {
        // Private mode: they type it again next time.
      }
      saveJoin(token, { key: res.data.rejoinKey, name: res.data.name });
      onJoined({ token: res.data.token, title: meeting.title, mic, camera });
    } catch (err) {
      if (
        saved &&
        (err as { response?: { status?: number } }).response?.status === 401
      ) {
        // The way back in no longer works; the passcode does.
        saveJoin(token, null);
        setSaved(null);
        setName(saved.name);
        setProblem("Enter the passcode to join again.");
      } else {
        setProblem(apiErrorMessage(err, "Could not join the meeting."));
      }
      setBusy(false);
    }
  };

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        void join();
      }}
    >
      <div className="space-y-1 text-center">
        <p className="text-sm text-muted-foreground">
          {meeting.organization
            ? `${meeting.organization} invites you to`
            : "You are invited to"}
        </p>
        <h1 className="break-anywhere text-xl font-semibold">
          {meeting.title}
        </h1>
        <When meeting={meeting} />
      </div>

      {saved ? (
        <p className="text-center text-sm" data-testid="guest-rejoin">
          You joined from this browser as{" "}
          <span className="font-medium">{saved.name}</span>.{" "}
          <button
            type="button"
            className="text-primary underline underline-offset-2"
            onClick={() => {
              saveJoin(token, null);
              setSaved(null);
            }}
          >
            Not you?
          </button>
        </p>
      ) : (
        <>
          <div className="space-y-2">
            <Label htmlFor="guestName">Your name</Label>
            <Input
              id="guestName"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={60}
              autoComplete="name"
              required
              autoFocus={!name}
            />
            <p className="text-xs text-muted-foreground">
              Everybody in the meeting sees it, marked as a guest.
            </p>
          </div>
          {meeting.passcodeRequired && (
            <div className="space-y-2">
              <Label htmlFor="guestPasscode">Passcode</Label>
              <Input
                id="guestPasscode"
                value={passcode}
                onChange={(e) => setPasscode(e.target.value)}
                autoComplete="off"
                inputMode="text"
                required
                autoFocus={!!name}
                className="font-mono tracking-wider"
              />
            </div>
          )}
        </>
      )}

      <div
        className="flex justify-center gap-2"
        role="group"
        aria-label="Join with"
      >
        <DeviceChoice
          on={mic}
          onToggle={() => setMic((v) => !v)}
          label="microphone"
          On={Mic}
          Off={MicOff}
        />
        <DeviceChoice
          on={camera}
          onToggle={() => setCamera((v) => !v)}
          label="camera"
          On={Video}
          Off={VideoOff}
        />
      </div>

      {problem && (
        <p className="text-sm text-destructive" role="alert">
          {problem}
        </p>
      )}
      <Button
        type="submit"
        className="w-full"
        disabled={busy || waiting || (!saved && !name.trim())}
      >
        {(busy || waiting) && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
        {waiting
          ? "Waiting for the meeting to start"
          : saved
            ? "Join again"
            : "Join the meeting"}
      </Button>
      {waiting && (
        <p className="text-center text-xs text-muted-foreground" role="status">
          You can join once somebody from{" "}
          {meeting.organization || "the organization"} has started it. This page
          lets you in when they do.
        </p>
      )}
    </form>
  );
}

function DeviceChoice({
  on,
  onToggle,
  label,
  On,
  Off,
}: {
  on: boolean;
  onToggle: () => void;
  label: string;
  On: React.ComponentType<{ className?: string }>;
  Off: React.ComponentType<{ className?: string }>;
}) {
  const Icon = on ? On : Off;
  return (
    <Button
      type="button"
      variant={on ? "secondary" : "outline"}
      onClick={onToggle}
      aria-pressed={on}
      aria-label={`Join with ${label} ${on ? "on" : "off"}`}
      className={cn("gap-2", !on && "text-muted-foreground")}
    >
      <Icon className="h-4 w-4" />
      <span className="capitalize">{label}</span>
    </Button>
  );
}

function AfterLeaving({
  meeting,
  left,
  onRejoin,
}: {
  meeting: GuestMeeting;
  left: Left;
  onRejoin: () => void;
}) {
  const text = {
    left: "You left the meeting.",
    removed: "The organizer removed you from the meeting.",
    ended: "The meeting has ended.",
    lost: "The connection to the meeting was lost.",
  }[left.reason];
  const canRejoin =
    (left.reason === "left" || left.reason === "lost") &&
    meeting.state !== "ended";
  return (
    <div className="space-y-4 text-center">
      <Notice title={meeting.title}>{text}</Notice>
      {canRejoin && (
        <Button onClick={onRejoin} variant="outline">
          Join again
        </Button>
      )}
    </div>
  );
}

/** The meeting itself, filling the window. */
function GuestRoom({
  joined,
  onLeft,
}: {
  joined: Joined;
  onLeft: (reason: Left["reason"]) => void;
}) {
  const [problem, setProblem] = useState("");
  return (
    <LiveKitRoom
      token={joined.token}
      serverUrl={liveKitURL()}
      connect
      audio={joined.mic}
      video={joined.camera}
      data-lk-theme="default"
      onDisconnected={(reason) => {
        if (reason === DisconnectReason.PARTICIPANT_REMOVED) onLeft("removed");
        else if (reason === DisconnectReason.ROOM_DELETED) onLeft("ended");
        else if (reason === DisconnectReason.CLIENT_INITIATED) onLeft("left");
        else onLeft("lost");
      }}
      onError={(err) => setProblem(err.message)}
      className="flex h-viewport flex-col bg-background text-foreground"
    >
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">
          {joined.title}
          <GuestCount />
        </span>
        <span className="hidden rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground sm:inline">
          You are a guest
        </span>
        <LeaveButton />
      </div>
      <BlockedAudioPrompt compact={false} />
      <NotesBanner asGuest />
      {problem && (
        <p className="border-b border-border bg-destructive/10 p-2 text-center text-sm text-destructive">
          {problem}
        </p>
      )}
      <div className="min-h-0 flex-1">
        <CallStage callId="" compact={false} asGuest />
      </div>
      <div className="flex shrink-0 flex-col items-center gap-1 border-t border-border p-2 sm:p-3">
        <CallControls />
        <ShareUnavailableNote />
      </div>
      <RoomAudioRenderer />
      <CameraBackground />
    </LiveKitRoom>
  );
}

function GuestCount() {
  const people = useParticipants().filter((p) => !isNotetaker(p));
  return <span className="text-muted-foreground"> · {people.length}</span>;
}

function LeaveButton() {
  const [leaving, setLeaving] = useState(false);
  const room = useRoomContext();
  return (
    <Button
      variant="destructive"
      size="sm"
      disabled={leaving}
      onClick={() => {
        setLeaving(true);
        void room.disconnect();
      }}
    >
      <LogOut className="mr-1 h-4 w-4" />
      Leave
    </Button>
  );
}
