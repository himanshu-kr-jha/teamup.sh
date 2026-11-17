import { useRef, useState, type ComponentType, type ReactNode } from 'react';
import { MediaDeviceMenu, useRoomContext, useTrackToggle } from '@livekit/components-react';
import { Track } from 'livekit-client';
import { Mic, MicOff, MonitorOff, MonitorUp, Video, VideoOff } from 'lucide-react';
import { useToast } from '@/shared/hooks/useToast';
import { BackgroundPicker } from './BackgroundPicker';
import { useMeetingChatPanel } from './MeetingChat';
import { cn } from '@/shared/utils/cn';
import { shortcutHint, type ShortcutId } from '@/shared/shortcuts/catalogue';
import { useShortcut } from '@/shared/shortcuts/ShortcutsProvider';

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

function useToggle(source: ToggleSource) {
  const toast = useToast();
  return useTrackToggle({
    source,
    onDeviceError: (error: Error) => {
      // Closing the browser's picker is changing your mind, not a failure.
      if (error.name === 'NotAllowedError' && source === Track.Source.ScreenShare) return;
      toast.error(
        source === Track.Source.ScreenShare
          ? 'Could not share your screen.'
          : 'TeamUp could not use that device. Check the permission in your browser.'
      );
    },
  });
}

function Toggle({ source, compact }: { source: ToggleSource; compact?: boolean }) {
  const { buttonProps, enabled } = useToggle(source);
  const { on, off, IconOn, IconOff, shortcut } = CONTROLS[source];
  const Icon = enabled ? IconOn : IconOff;
  const label = enabled ? on : off;
  return (
    <button
      {...buttonProps}
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
    </button>
  );
}

function WithDeviceMenu({ source, kind, label }: { source: ToggleSource; kind: MediaDeviceKind; label: string }) {
  return (
    <div className="lk-button-group">
      <Toggle source={source} />
      <div className="lk-button-group-menu">
        <MediaDeviceMenu kind={kind} aria-label={label} title={label} />
      </div>
    </div>
  );
}

/** The full set, under the call. Anybody in a call can share (decision 20). */
export function CallControls({ children }: { children?: ReactNode }) {
  const [shareable] = useState(canShareScreen);
  return (
    <div className="flex flex-wrap items-center justify-center gap-2" role="group" aria-label="Call controls">
      <WithDeviceMenu source={Track.Source.Microphone} kind="audioinput" label="Choose a microphone" />
      <WithDeviceMenu source={Track.Source.Camera} kind="videoinput" label="Choose a camera" />
      <BackgroundPicker />
      {shareable && <Toggle source={Track.Source.ScreenShare} />}
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
 * Meet; the browser keeps Ctrl+Shift+C for inspecting the page), and
 * Space held to talk while muted. Bound only while a call is open, so the
 * browser keeps Ctrl+D for bookmarks the rest of the time.
 */
export function CallShortcuts() {
  const room = useRoomContext();
  const chat = useMeetingChatPanel();
  const toast = useToast();
  // Space turned the microphone on, so letting go of it turns it off again.
  const talking = useRef(false);

  const deviceFailed = () => toast.error('TeamUp could not use that device. Check the permission in your browser.');
  const me = () => room.localParticipant;

  useShortcut('call.mic', () => {
    void me().setMicrophoneEnabled(!me().isMicrophoneEnabled).catch(deviceFailed);
  });
  useShortcut('call.camera', () => {
    void me().setCameraEnabled(!me().isCameraEnabled).catch(deviceFailed);
  });
  useShortcut('call.share', () => {
    if (!canShareScreen()) return false;
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
  useShortcut(
    'call.talk',
    () => {
      if (me().isMicrophoneEnabled) return false;
      talking.current = true;
      void me().setMicrophoneEnabled(true).catch(deviceFailed);
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
