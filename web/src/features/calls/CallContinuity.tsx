import { useCallback, useEffect } from 'react';
import { useRoomContext } from '@livekit/components-react';
import { ConnectionState, RoomEvent } from 'livekit-client';
import { currentBackgroundChoice } from './background';
import { REMEMBER_EVERY_MS, rememberCall } from './callMemory';
import { putCallsListUnderneath } from './historyGuard';

// What keeps a call going through a reload, the Back button and a tab about to
// close (callMemory.ts holds the part that is not React).

let leavingPage = false;

/**
 * Whether this page is going away: a reload, a closed tab, a link to another site.
 * The room closes then too, and that is not you leaving the call: nothing is
 * forgotten and nobody is moved to another page.
 */
export const isLeavingPage = () => leavingPage;

/**
 * Writes down the call while you are in it, with your microphone, camera and
 * devices as they are now, and says goodbye to the room when the page goes.
 * Renders nothing; it sits inside the room.
 */
export function RememberCall({
  userId,
  callId,
  standing,
  companion = false,
  soundOff = false,
  joiningWith,
}: {
  userId: string | undefined;
  callId: string;
  standing: boolean;
  /** A second camera whose sound is still off: a reload keeps it so. */
  companion?: boolean;
  /** A second camera's sound, off or turned on. */
  soundOff?: boolean;
  /** The microphone and camera the call is being joined with, until it is connected and they can be read. */
  joiningWith: { mic: boolean; camera: boolean };
}) {
  const room = useRoomContext();

  const joiningMic = joiningWith.mic;
  const joiningCamera = joiningWith.camera;
  const write = useCallback(() => {
    // Written from the moment the call is being joined, not only once it is
    // connected: a reload while it is still connecting (a second, more on a
    // slow phone) used to find nothing written and drop you at the screen
    // before the call. Until then the microphone and camera are what you
    // joined with, since none is published yet.
    if (!userId || room.state === ConnectionState.Disconnected) return;
    const connected = room.state === ConnectionState.Connected;
    const me = room.localParticipant;
    const device = (kind: MediaDeviceKind) => {
      const id = room.getActiveDevice(kind);
      return id && id !== 'default' ? id : undefined;
    };
    rememberCall({
      userId,
      callId,
      standing,
      companion,
      soundOff,
      mic: connected ? me.isMicrophoneEnabled : joiningMic,
      camera: connected ? me.isCameraEnabled : joiningCamera,
      audioInput: device('audioinput'),
      videoInput: device('videoinput'),
      audioOutput: device('audiooutput'),
      background: currentBackgroundChoice(),
      at: Date.now(),
    });
  }, [room, userId, callId, standing, companion, soundOff, joiningMic, joiningCamera]);

  useEffect(() => {
    const events = [
      RoomEvent.ConnectionStateChanged,
      RoomEvent.Connected,
      RoomEvent.Reconnected,
      RoomEvent.LocalTrackPublished,
      RoomEvent.LocalTrackUnpublished,
      RoomEvent.TrackMuted,
      RoomEvent.TrackUnmuted,
      RoomEvent.ActiveDeviceChanged,
    ] as const;
    events.forEach((e) => room.on(e, write));
    write();
    // Kept fresh, so a reload an hour into the call is still moments after.
    const timer = setInterval(write, REMEMBER_EVERY_MS);

    // LiveKit's own goodbye (disconnectOnPageLeave) runs on beforeunload, which
    // fires before the browser asks whether to leave: answering Stay then left you
    // in a call that had already hung up. pagehide comes only once the page is
    // really going.
    const onPageHide = () => {
      write();
      leavingPage = true;
      void room.disconnect();
    };
    // A page put away whole and brought back (the back-forward cache) has lost its
    // room meanwhile; loading it again goes back into the call.
    const onPageShow = (e: PageTransitionEvent) => {
      if (e.persisted && leavingPage) window.location.reload();
    };
    window.addEventListener('pagehide', onPageHide);
    window.addEventListener('pageshow', onPageShow);
    return () => {
      events.forEach((e) => room.off(e, write));
      clearInterval(timer);
      window.removeEventListener('pagehide', onPageHide);
      window.removeEventListener('pageshow', onPageShow);
    };
  }, [room, write]);

  // Closing the tab or going to another site asks first, in the browser's own
  // words. A reload asks too, and coming back from it goes straight into the call.
  useEffect(() => {
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // Older browsers only ask when this is set.
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, []);

  return null;
}

/**
 * On the call's own page, Back takes you back in the app with the call shrunk
 * into the corner, never out of it: the room lives above the router, so going
 * back is only another page. Where the call's page is the first this tab opened,
 * Back would leave the site, or on a phone close the app, and the call with it.
 * An entry for the calls list is put underneath it instead, so Back goes there.
 * The entry is written by hand (historyGuard.ts), so the page on screen does not
 * change meanwhile.
 */
export function useBackStaysInApp(onCallPage: boolean) {
  useEffect(() => {
    if (onCallPage) putCallsListUnderneath();
  }, [onCallPage]);
}
