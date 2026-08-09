import { useEffect, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useWebSocket } from '@/features/websocket/WSProvider';
import {
  LiveKitRoom,
  GridLayout,
  ParticipantTile,
  RoomAudioRenderer,
  ControlBar,
  useTracks,
  useRoomContext,
} from '@livekit/components-react';
import { Track, RoomEvent } from 'livekit-client';
import '@livekit/components-styles';
import { AlertTriangle, Loader2, PhoneOff } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { callsApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';

/**
 * The real call. This page used to render the words "LiveKit Call
 * Integration" and nothing else, with the SDK installed and unused.
 *
 * The server issues a token scoped to this call's room, refuses ended calls,
 * and records attendance when the token is handed out.
 */
export function CallViewPage() {
  const { roomName: callId } = useParams<{ roomName: string }>();
  const navigate = useNavigate();
  const toast = useToast();
  const { on } = useWebSocket();

  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  const serverUrl = import.meta.env.VITE_LIVEKIT_URL || inferLiveKitURL();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await callsApi.getToken(callId!);
        if (!cancelled) setToken(res.data.token);
      } catch (err) {
        if (!cancelled) setError(apiErrorMessage(err, 'Could not join this call.'));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [callId]);

  const leave = () => navigate('/calls');

  // The other side turning the call down, or never picking it up, has to reach
  // the caller: otherwise they sit in an empty room with no idea why.
  useEffect(() => {
    return on('call.ended', (msg) => {
      const payload = msg.payload as any;
      if (payload?.callId !== callId) return;
      const who = payload.declinedBy ? `${payload.declinedBy} ` : '';
      if (payload.reason === 'no_answer') {
        toast.toast(`${who ? who + 'did not answer.' : 'Nobody answered.'}`);
      } else if (payload.reason === 'declined') {
        toast.toast(`${who}declined the call.`);
      } else {
        toast.toast('The call has ended.');
      }
      navigate('/calls');
    });
  }, [on, callId, navigate, toast]);

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (error || !token) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-md text-center">
          <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-destructive" />
          <p className="font-medium">{error || 'This call is not available.'}</p>
          <Button className="mt-4" onClick={leave}>Back to calls</Button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col bg-background">
      <LiveKitRoom
        token={token}
        serverUrl={serverUrl}
        connect
        video
        audio
        data-lk-theme="default"
        className="flex h-full flex-col"
        onDisconnected={leave}
        onError={(err) => {
          // A connection failure must say so rather than leave the user
          // staring at an empty room.
          toast.error(err.message || 'Lost connection to the call.');
        }}
      >
        <CallStage callId={callId!} onLeave={leave} />
        <RoomAudioRenderer />
      </LiveKitRoom>
    </div>
  );
}

function CallStage({ callId, onLeave }: { callId: string; onLeave: () => void }) {
  const room = useRoomContext();
  const toast = useToast();
  const [mediaError, setMediaError] = useState('');

  // Camera and microphone permission is the most common thing to go wrong;
  // an empty tile with no explanation is the worst possible answer.
  useEffect(() => {
    const onFailure = () => {
      setMediaError(
        'TeamUp could not use your camera or microphone. Check the permission in your browser and rejoin.'
      );
    };
    room.on(RoomEvent.MediaDevicesError, onFailure);
    return () => {
      room.off(RoomEvent.MediaDevicesError, onFailure);
    };
  }, [room]);

  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: false }
  );

  const end = async () => {
    try {
      await callsApi.end(callId);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not end the call for everyone.'));
    } finally {
      await room.disconnect();
      onLeave();
    }
  };

  return (
    <>
      {mediaError && (
        <div className="flex items-start gap-3 border-b border-border bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
          <span>{mediaError}</span>
        </div>
      )}

      <div className="min-h-0 flex-1">
        <GridLayout tracks={tracks} className="h-full">
          <ParticipantTile />
        </GridLayout>
      </div>

      <div className="flex items-center justify-between gap-4 border-t border-border p-3">
        {/* Microphone, camera, screen share and leave all come from the SDK. */}
        <ControlBar variation="minimal" controls={{ leave: false }} />
        <Button variant="destructive" onClick={end}>
          <PhoneOff className="mr-2 h-4 w-4" />
          End call
        </Button>
      </div>
    </>
  );
}

/**
 * The browser needs a websocket URL for LiveKit. In the default deployment it
 * sits alongside the app, so derive it rather than requiring configuration.
 */
function inferLiveKitURL(): string {
  const { protocol, hostname } = window.location;
  const scheme = protocol === 'https:' ? 'wss' : 'ws';
  return `${scheme}://${hostname}:7880`;
}
