import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { Archive, CalendarClock, ClipboardList, Loader2, Pencil, Plus, Video } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent } from '@/shared/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { useAuth } from '@/features/auth/AuthProvider';
import { standingApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useActiveCall } from '../ActiveCallProvider';
import { StandingMeetingDialog } from './StandingMeetingDialog';
import { describeSchedule, type StandingMeeting } from './standing';

/** Goes into today's room of a standing meeting. Shared with the calendar. */
export function useJoinStanding() {
  const { enter } = useActiveCall();
  const navigate = useNavigate();
  const toast = useToast();
  const [joining, setJoining] = useState<string | null>(null);

  const joinStanding = async (id: string) => {
    setJoining(id);
    try {
      const { data } = await standingApi.join(id);
      enter(data.callId, data.token);
      navigate(`/calls/${data.callId}`);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not join this meeting.'));
    } finally {
      setJoining(null);
    }
  };
  return { joinStanding, joining };
}

/** The permanent rooms, pinned at the top of Calls. */
export function StandingMeetings() {
  const { user } = useAuth();
  const isAdmin = user?.roles?.some((r) => r === 'owner' || r === 'admin') ?? false;
  const queryClient = useQueryClient();
  const toast = useToast();
  const { joinStanding, joining } = useJoinStanding();

  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['standing-meetings'],
    queryFn: () => standingApi.list().then((res) => res.data),
    staleTime: 30_000,
  });

  const [editing, setEditing] = useState<StandingMeeting | null>(null);
  const [creating, setCreating] = useState(false);
  const [archiving, setArchiving] = useState<StandingMeeting | null>(null);
  const [busyArchiving, setBusyArchiving] = useState(false);

  const meetings = data ?? [];

  // Somebody with nothing to see and nothing they can do loses nothing by this
  // section not being there.
  if (!isPending && !error && meetings.length === 0 && !isAdmin) return null;

  const archive = async () => {
    if (!archiving) return;
    setBusyArchiving(true);
    try {
      await standingApi.archive(archiving.id);
      await queryClient.invalidateQueries({ queryKey: ['standing-meetings'] });
      await queryClient.invalidateQueries({ queryKey: ['calendar'] });
      toast.success(`${archiving.name} was archived. Its attendance is kept.`);
      setArchiving(null);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not archive that meeting.'));
    } finally {
      setBusyArchiving(false);
    }
  };

  return (
    <section aria-labelledby="standingHeading" className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h2 id="standingHeading" className="flex items-center gap-2 text-sm font-semibold">
          <CalendarClock className="h-4 w-4 text-primary" aria-hidden="true" />
          Standing meetings
        </h2>
        {isAdmin && (
          <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
            <Plus className="mr-1 h-4 w-4" aria-hidden="true" />
            New
          </Button>
        )}
      </div>

      {isPending ? (
        <div role="status" className="flex items-center gap-2 py-3 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
          Loading standing meetings
        </div>
      ) : error ? (
        <div role="alert" className="flex flex-wrap items-center gap-3 rounded-lg border border-border px-3 py-2 text-sm">
          {apiErrorMessage(error, 'Could not load standing meetings.')}
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            Try again
          </Button>
        </div>
      ) : meetings.length === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-3 py-4 text-center text-sm text-muted-foreground">
          No standing meetings yet. Make one for the morning scrum, and everybody will have one room to join.
        </p>
      ) : (
        <div className="grid gap-2 lg:grid-cols-2">
          {meetings.map((meeting) => (
            <Card key={meeting.id} className="border-primary/30">
              <CardContent className="flex flex-col gap-2 p-3 sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1">
                  <h3 className="break-anywhere font-semibold">{meeting.name}</h3>
                  <p className="text-sm text-muted-foreground">{describeSchedule(meeting)}</p>
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-1">
                  {isAdmin && (
                    <>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-9 w-9"
                        aria-label={`Edit ${meeting.name}`}
                        title="Edit"
                        onClick={() => setEditing(meeting)}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-9 w-9"
                        aria-label={`Archive ${meeting.name}`}
                        title="Archive"
                        onClick={() => setArchiving(meeting)}
                      >
                        <Archive className="h-4 w-4" />
                      </Button>
                    </>
                  )}
                  <Button variant="outline" size="sm" asChild>
                    <Link to={`/calls/standing/${meeting.id}/attendance`} aria-label={`Attendance for ${meeting.name}`}>
                      <ClipboardList className="mr-1 h-4 w-4" aria-hidden="true" />
                      Attendance
                    </Link>
                  </Button>
                  <Button
                    size="sm"
                    disabled={joining === meeting.id}
                    aria-label={`Join ${meeting.name}`}
                    onClick={() => void joinStanding(meeting.id)}
                  >
                    {joining === meeting.id ? (
                      <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" />
                    ) : (
                      <Video className="mr-1 h-4 w-4" aria-hidden="true" />
                    )}
                    Join
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <StandingMeetingDialog
        open={creating || !!editing}
        meeting={editing}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false);
            setEditing(null);
          }
        }}
      />

      <Dialog open={!!archiving} onOpenChange={(open) => !open && setArchiving(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Archive {archiving?.name}?</DialogTitle>
            <DialogDescription>
              The room goes away for everybody and it leaves their calendars. Attendance already saved is kept.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setArchiving(null)} disabled={busyArchiving}>
              Keep it
            </Button>
            <Button variant="destructive" onClick={archive} disabled={busyArchiving}>
              {busyArchiving && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
              Archive
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
