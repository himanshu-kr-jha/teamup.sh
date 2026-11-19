import { useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';
import { standingApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useActiveCall } from '../ActiveCallProvider';
import { PreJoinScreen } from '../PreJoinScreen';
import type { JoinChoice } from '../prejoin';
import { thisDevice } from '../devices';

/**
 * The screen before today's room of a standing meeting, reached from its Join
 * button on Calls or the calendar.
 *
 * Opening the day's room is what starts the meeting and what attendance counts as
 * having asked to join (standing.go, openOccurrence), so it is asked for only when
 * Join is pressed here: somebody who looks and goes Back has opened nothing.
 */
export function StandingPreJoinPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const toast = useToast();
  const { activeCall, enter } = useActiveCall();

  const { data: meeting, isPending } = useQuery({
    queryKey: ['standing-meetings'],
    queryFn: () => standingApi.list().then((res) => res.data),
    select: (list) => list.find((m) => m.id === id),
    staleTime: 30_000,
  });

  if (!id) return null;
  if (isPending) {
    return (
      <div role="status" aria-label="Loading the meeting" className="flex h-full items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" aria-hidden="true" />
      </div>
    );
  }

  const join = async (choice: JoinChoice) => {
    try {
      const { data } = await standingApi.join(id, thisDevice());
      // Already in today's room: back to it, rather than into it a second time.
      if (activeCall?.callId !== data.callId) {
        // Already in it from another device: asked how this one joins.
        enter(
          data.callId,
          data.token,
          {
            mic: choice.mic,
            camera: choice.camera,
            audioInput: choice.audioInput,
            videoInput: choice.videoInput,
            audioOutput: choice.audioOutput,
            standing: true,
          },
          data.otherDevices
        );
      }
      navigate(`/calls/${data.callId}`, { replace: true });
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not join this meeting.'));
      throw err;
    }
  };

  return <PreJoinScreen call={{ title: meeting?.name }} lobby="none" onJoin={join} />;
}
