import { useRef, useState, type ComponentType, type ReactNode } from 'react';
import { MediaDeviceMenu, useConnectionState, useIsSpeaking, useLocalParticipant, useRoomContext, useTrackToggle } from '@livekit/components-react';
import { ConnectionState, Track } from 'livekit-client';
import { Mic, MicOff, MonitorOff, MonitorUp, Video, VideoOff } from 'lucide-react';
import { useToast } from '@/shared/hooks/useToast';
import { BackgroundPicker } from './BackgroundPicker';
import { MicrophoneMenu } from './NoiseControls';
import { useMeetingChatPanel } from './MeetingChat';
import { cn } from '@/shared/utils/cn';
import { shortcutHint, type ShortcutId } from '@/shared/shortcuts/catalogue';
import { useShortcut } from '@/shared/shortcuts/ShortcutsProvider';
import { HandButton, useHands } from './Hands';
import { SpeakingBars } from './SpeakingBars';
import { deviceProblem, inCallText, wasReported, type InputDevice } from './mediaErrors';

/** Whether this browser can capture a screen at all. Most phone browsers cannot. */
export function canShareScreen(): boolean {
  return typeof navigator !== 'undefined' && !!navigator.mediaDevices && !!navigator.mediaDevices.getDisplayMedia;
}

type ToggleSource = Track.Source.Microphone | Track.Source.Camera | Track.Source.ScreenShare;

const CONTROLS: Record<
  ToggleSource,
  {
    on: string;
    off: string;
    IconOn: ComponentType<{ className?: string }>;
    IconOff: ComponentType<{ className?: string }>;
    shortcut: ShortcutId;
  }
> = {
  [Track.Source.Microphone]: { on: 'Turn your microphone off', off: 'Turn your microphone on', IconOn: Mic, IconOff: MicOff, shortcut: 'call.mic' },
  [Track.Source.Camera]: { on: 'Turn your camera off', off: 'Turn your camera on', IconOn: Video, IconOff: VideoOff, shortcut: 'call.camera' },
  [Track.Source.ScreenShare]: { on: 'Stop sharing your screen', off: 'Share your screen', IconOn: MonitorOff, IconOff: MonitorUp, shortcut: 'call.share' },
};

/**
 * A camera or microphone that would not come on. The first time it is turned on
 * the banner over the call says why (MediaErrorBanner); turning it back on after
 * it was off fails only here, so that is a toast in the same words.
 */
function deviceFailed(say: (text: string) => void, error: unknown, device: InputDevice) {
  if (!wasReported(error)) say(inCallText(deviceProblem(error), device));
}

function useToggle(source: ToggleSource) {
  const toast = useToast();
  return useTrackToggle({
    source,
    onDeviceError: (error: Error) => {
      if (source === Track.Source.ScreenShare) {
        // Closing the browser's picker is changing your mind, not a failure.
        if (error.name !== 'NotAllowedError') toast.error('Could not share your screen.');
        return;
      }
      deviceFailed(toast.error, error, source === Track.Source.Camera ? 'camera' : 'microphone');
    },
  });
}

/**
 * Whether the call has connected, so the camera, microphone and screen can be
 * turned on and off. Until then LiveKit is still to turn them on or off as you
 * joined, once it is signed in to the room: anything pressed before that was
 * undone a moment later, a camera turned on going off again by itself.
 */
function useCanToggle(): boolean {
  const room = useRoomContext();
  return useConnectionState(room) === ConnectionState.Connected;
}

function Toggle({ source, compact }: { source: ToggleSource; compact?: boolean }) {
  const { buttonProps, enabled } = useToggle(source);
  const ready = useCanToggle();
  const { on, off, IconOn, IconOff, shortcut } = CONTROLS[source];
  const Icon = enabled ? IconOn : IconOff;
  const label = enabled ? on : off;
  return (
    <button
      {...buttonProps}
      disabled={buttonProps.disabled || !ready}
      type="button"
      aria-label={label}
      {...shortcutHint(shortcut, label)}
      className={cn(
        compact
          ? cn(
              'inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-accent disabled:opacity-50',
              enabled && source === Track.Source.ScreenShare && 'bg-primary text-primary-foreground hover:bg-primary/90'
            )
          : buttonProps.className
      )}
    >
      <Icon className={compact ? 'h-4 w-4' : 'h-5 w-5'} aria-hidden="true" />
      {/* Your voice going out, beside the microphone that carries it. */}
      {!compact && source === Track.Source.Microphone && enabled && <OwnVoice />}
    </button>
  );
}

/** Moving bars for whatever your own microphone hears. */
function OwnVoice() {
  const { localParticipant } = useLocalParticipant();
  const speaking = useIsSpeaking(localParticipant);
  return <SpeakingBars participant={localParticipant} speaking={speaking} muted={false} bare always size="md" className="-ml-0.5 h-5" />;
}

function WithDeviceMenu({ source, kind, label, menu }: { source: ToggleSource; kind: MediaDeviceKind; label: string; menu?: ReactNode }) {
  return (
    <div className="lk-button-group">
      <Toggle source={source} />
      <div className="lk-button-group-menu">
        {menu ?? <MediaDeviceMenu kind={kind} aria-label={label} title={label} />}
      </div>
    </div>
  );
}

/**
 * The full set, under the call. Anybody in a call can share (decision 20).
 *
 * On a phone they wrap onto a second row. LiveKit makes a button group as tall as
 * its container, which in a row that wraps is the height of both rows, so the
 * microphone and camera grew to double height and pushed the last row off the
 * bottom of the screen; here a group is only as tall as its buttons.
 */
export function CallControls({ children }: { children?: ReactNode }) {
  const [shareable] = useState(canShareScreen);
  return (
    <div
      className="flex flex-wrap items-center justify-center gap-2 [&_.lk-button-group]:h-auto"
      role="group"
      aria-label="Call controls"
    >
      {/* Which microphone, and noise cancellation. */}
      <WithDeviceMenu
        source={Track.Source.Microphone}
        kind="audioinput"
        label="Choose a microphone"
        menu={<MicrophoneMenu label="Choose a microphone" />}
      />
      <WithDeviceMenu source={Track.Source.Camera} kind="videoinput" label="Choose a camera" />
      <BackgroundPicker />
      {shareable && <Toggle source={Track.Source.ScreenShare} />}
      <HandButton />
      {children}
    </div>
  );
}

/** The same three in the title bar of the corner window, where there is no room for more. */
export function CompactCallControls() {
  const [shareable] = useState(canShareScreen);
  return (
    <div className="flex items-center gap-0.5" role="group" aria-label="Call controls">
      <Toggle source={Track.Source.Microphone} compact />
      <Toggle source={Track.Source.Camera} compact />
      {shareable && <Toggle source={Track.Source.ScreenShare} compact />}
    </div>
  );
}

/** Says why there is no share button, since nothing else would (decision 21). */
export function ShareUnavailableNote() {
  const [shareable] = useState(canShareScreen);
  if (shareable) return null;
  return <p className="text-center text-xs text-muted-foreground">This browser cannot share a screen. A computer can.</p>;
}

/**
 * The call's keys: Ctrl+D for the microphone and Ctrl+E for the camera, as in
 * other meeting apps, Ctrl+Shift+S to share, Ctrl+Alt+C for the chat (as in Google
 * Meet; the browser keeps Ctrl+Shift+C for inspecting the page), Ctrl+Shift+H
 * to raise or lower your hand, and
 * Space held to talk while muted. Bound only while a call is open, so the
 * browser keeps Ctrl+D for bookmarks the rest of the time.
 */
export function CallShortcuts() {
  const room = useRoomContext();
  const chat = useMeetingChatPanel();
  const hands = useHands();
  const toast = useToast();
  // Space turned the microphone on, so letting go of it turns it off again.
  const talking = useRef(false);

  const micFailed = (error: unknown) => deviceFailed(toast.error, error, 'microphone');
  const me = () => room.localParticipant;
  // As the buttons: not until the call has connected (useCanToggle).
  const ready = useCanToggle();

  useShortcut('call.mic', () => {
    if (!ready) return false;
    void me().setMicrophoneEnabled(!me().isMicrophoneEnabled).catch(micFailed);
  });
  useShortcut('call.camera', () => {
    if (!ready) return false;
    void me()
      .setCameraEnabled(!me().isCameraEnabled)
      .catch((error: unknown) => deviceFailed(toast.error, error, 'camera'));
  });
  useShortcut('call.share', () => {
    if (!ready || !canShareScreen()) return false;
    void me()
      .setScreenShareEnabled(!me().isScreenShareEnabled)
      .catch((error: Error) => {
        // Closing the browser's picker is changing your mind, not a failure.
        if (error.name !== 'NotAllowedError') toast.error('Could not share your screen.');
      });
  });
  useShortcut('call.chat', () => {
    if (!chat) return false;
    chat.setOpen(!chat.open);
  });
  useShortcut('call.hand', () => {
    if (!hands?.canRaise) return false;
    hands.toggle();
  });
  useShortcut(
    'call.talk',
    () => {
      if (!ready || me().isMicrophoneEnabled) return false;
      talking.current = true;
      void me().setMicrophoneEnabled(true).catch(micFailed);
    },
    {
      release: () => {
        if (!talking.current) return;
        talking.current = false;
        void me().setMicrophoneEnabled(false);
      },
    }
  );
  return null;
}
