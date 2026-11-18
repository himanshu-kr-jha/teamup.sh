import { useCallback, useEffect, useRef } from 'react';
import { useRoomContext } from '@livekit/components-react';
import { ConnectionState, RoomEvent, type Room } from 'livekit-client';
import { currentBackgroundChoice } from './background';
import { LEAVING_TOPIC, REMEMBER_EVERY_MS, createDropWatch, rememberCall } from './callMemory';

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
export function RememberCall({ userId, callId, standing }: { userId: string | undefined; callId: string; standing: boolean }) {
  const room = useRoomContext();

  const write = useCallback(() => {
    if (!userId || room.state !== ConnectionState.Connected) return;
    const me = room.localParticipant;
    const device = (kind: MediaDeviceKind) => {
      const id = room.getActiveDevice(kind);
      return id && id !== 'default' ? id : undefined;
    };
    rememberCall({
      userId,
      callId,
      standing,
      mic: me.isMicrophoneEnabled,
      camera: me.isCameraEnabled,
      audioInput: device('audioinput'),
      videoInput: device('videoinput'),
      audioOutput: device('audiooutput'),
      background: currentBackgroundChoice(),
      at: Date.now(),
    });
  }, [room, userId, callId, standing]);

  useEffect(() => {
    const events = [
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
 *
 * Written into the browser's history directly, in the form React Router writes
 * its own (it reads its position back from there on every change), so the page
 * on screen does not change meanwhile.
 */
export function useBackStaysInApp(onCallPage: boolean) {
  useEffect(() => {
    if (!onCallPage) return;
    const state = window.history.state as { usr?: unknown; key?: string; idx?: number } | null;
    if (!state || state.idx !== 0) return;
    const here = window.location.pathname + window.location.search + window.location.hash;
    try {
      window.history.replaceState({ usr: null, key: Math.random().toString(36).slice(2, 10), idx: 0 }, '', '/calls');
      window.history.pushState({ ...state, idx: 1 }, '', here);
    } catch {
      // A browser limiting history writes: Back then leaves, as it always did.
    }
  }, [onCallPage]);
}

/**
 * Who dropped out of the call without saying they were leaving, and so may be
 * about to come back from a reload (createDropWatch). Leaving deliberately says
 * so first (sayLeaving).
 */
export function useDropWatch(room: Room) {
  const watch = useRef(createDropWatch());
  useEffect(() => {
    const w = watch.current;
    const onData = (_payload: Uint8Array, from?: { identity: string }, _kind?: unknown, topic?: string) => {
      if (topic === LEAVING_TOPIC && from) w.saidLeaving(from.identity);
    };
    const onLeft = (p: { identity: string }) => w.left(p.identity, Date.now());
    const onJoined = (p: { identity: string }) => w.joined(p.identity);
    room.on(RoomEvent.DataReceived, onData);
    room.on(RoomEvent.ParticipantDisconnected, onLeft);
    room.on(RoomEvent.ParticipantConnected, onJoined);
    return () => {
      room.off(RoomEvent.DataReceived, onData);
      room.off(RoomEvent.ParticipantDisconnected, onLeft);
      room.off(RoomEvent.ParticipantConnected, onJoined);
    };
  }, [room]);
  return useCallback(() => watch.current.awaited(Date.now()), []);
}

/**
 * Tells the others you are leaving on purpose, so they do not wait on you as on
 * somebody reloading. Never holds up leaving for more than a moment.
 */
export async function sayLeaving(room: Room): Promise<void> {
  if (room.state !== ConnectionState.Connected || room.remoteParticipants.size === 0) return;
  try {
    await Promise.race([
      room.localParticipant.publishData(new Uint8Array([1]), { reliable: true, topic: LEAVING_TOPIC }),
      new Promise((resolve) => setTimeout(resolve, 500)),
    ]);
  } catch {
    // Not allowed to send data, or already disconnected: they wait a moment longer.
  }
}
