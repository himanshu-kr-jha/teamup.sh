import type { RoomOptions } from 'livekit-client';

/**
 * How every call connects to LiveKit, for members and guests alike.
 *
 * Each person sends their camera once to LiveKit, which forwards it to the
 * others, as Google Meet does. Left at livekit-client's defaults, it forwarded
 * every camera to everybody at full size, those on other pages of the grid and
 * the thumbnails beside a shared screen included: in a meeting of 45 that is
 * 44 HD videos to each person, more than most connections carry, and several
 * gigabits a second from one server.
 *
 * adaptiveStream asks LiveKit only for the video a person can see, at the size
 * it is shown, and pauses it while the tab is in the background. dynacast stops
 * each camera sending the sizes nobody is watching, which saves the sender's
 * upload. A shared screen shown large still comes at full quality.
 *
 * One object, made once: LiveKitRoom builds a new room when its options change.
 */
export const ROOM_OPTIONS: RoomOptions = {
  adaptiveStream: true,
  dynacast: true,
};
