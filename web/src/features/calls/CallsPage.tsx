import { useState } from 'react';
import { AlertTriangle, Video, Phone, Users, Search, Loader2, Clock, CheckCircle, XCircle, History, Link2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Card, CardContent } from '@/shared/components/ui/card';
import { Badge } from '@/shared/components/ui/badge';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { useQuery } from '@tanstack/react-query';
import { callsApi } from '@/shared/api/client';
import { useNavigate } from 'react-router-dom';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { JOIN_EARLY_MINUTES, meetingStillOn } from '@/features/calendar/layout';
import { CallLogDialog } from './CallLogDialog';
import { CreateCallDialog } from './CreateCallDialog';
import { copyCallLink } from './copyLink';
import { StandingMeetings } from './standing/StandingMeetings';

interface Call {
  id: string;
  roomName: string;
  title: string;
  callType: 'video' | 'audio';
  status: 'scheduled' | 'active' | 'ended' | 'cancelled';
  channelId?: string;
  channelName?: string;
  createdBy: string;
  createdByName: string;
  scheduledStart?: string;
  scheduledEnd?: string;
  standingMeetingId?: string;
  canReadLog?: boolean;
  startedAt?: string;
  endedAt?: string;
  durationSeconds?: number;
  participants?: { userId: string; userName: string; userAvatar?: string; role: string }[];
  createdAt: string;
}

const statusConfig = {
  scheduled: { color: 'bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-200', icon: Clock },
  active: { color: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-200', icon: Video },
  ended: { color: 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200', icon: CheckCircle },
  cancelled: { color: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-200', icon: XCircle },
};

export function CallsPage() {
  const navigate = useNavigate();
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [createOpen, setCreateOpen] = useState(false);

  const { data: callsResponse, isLoading, error, refetch } = useQuery({
    queryKey: ['calls', { search: searchQuery, status: statusFilter }],
    queryFn: () => callsApi.list({ status: statusFilter !== 'all' ? statusFilter : undefined }),
  });

  const calls = callsResponse?.data || [];

  const filteredCalls = calls.filter((call) => {
    // A call needs no title, so match defensively.
    if (searchQuery && !(call.title ?? '').toLowerCase().includes(searchQuery.toLowerCase())) return false;
    if (statusFilter !== 'all' && call.status !== statusFilter) return false;
    return true;
  });

  return (
    <div className="flex h-full flex-col bg-background">
      {/* Header */}
      <div className="flex h-16 shrink-0 items-center justify-between gap-2 border-b border-border px-4 md:px-6">
        <div className="flex items-center gap-4">
          <h1 className="text-2xl font-bold">Calls</h1>
        </div>
        <div className="flex min-w-0 items-center gap-2 md:gap-4">
          <div className="relative hidden sm:block">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search calls..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="pl-10 w-64"
            />
          </div>
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            aria-label="Filter calls by status"
            className="min-w-0 rounded-md border border-input bg-background px-2 py-2 text-sm md:px-3"
          >
            <option value="all">All Status</option>
            <option value="scheduled">Scheduled</option>
            <option value="active">Active</option>
            <option value="ended">Ended</option>
            <option value="cancelled">Cancelled</option>
          </select>
          <Button className="shrink-0" onClick={() => setCreateOpen(true)}>New Call</Button>
        </div>
      </div>

      {/* Calls List */}
      <div className="flex-1 space-y-6 overflow-auto p-4 md:p-6">
        <StandingMeetings />
        {isLoading ? (
          <div role="status" aria-label="Loading calls" className="flex h-48 items-center justify-center"><Loader2 className="h-8 w-8 animate-spin" aria-hidden="true" /></div>
        ) : error ? (
          <div role="alert" className="flex h-48 flex-col items-center justify-center gap-3 text-center text-sm">
            <AlertTriangle className="h-6 w-6 text-muted-foreground" aria-hidden="true" />
            <p>{apiErrorMessage(error, 'Could not load your calls.')}</p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>Try again</Button>
          </div>
        ) : filteredCalls.length === 0 ? (
          <div className="flex h-48 flex-col items-center justify-center text-center text-muted-foreground">
            <Video className="h-12 w-12 mb-4 opacity-50" />
            <p className="text-lg">No calls found</p>
            <p className="text-sm">Create a new call or check your filters</p>
          </div>
        ) : (
          <div className="space-y-4">
            {filteredCalls.map((call) => (
              <CallCard key={call.id} call={call} onJoin={(id) => navigate(`/calls/${id}`, { state: { join: true } })} />
            ))}
          </div>
        )}
      </div>
      <CreateCallDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(id) => navigate(`/calls/${id}`)}
      />
    </div>
  );
}

/**
 * Whether Join is on offer. Joining opens a scheduled meeting, and a meeting that
 * has opened can no longer be moved, so it is not offered long before its time.
 */
function joinable(call: Call, now: Date = new Date()): boolean {
  const stillOn = meetingStillOn(call, now);
  if ((call.status === 'ended' && !stillOn) || call.status === 'cancelled') return false;
  if ((call.status !== 'scheduled' && !stillOn) || !call.scheduledStart) return true;
  return now.getTime() >= new Date(call.scheduledStart).getTime() - JOIN_EARLY_MINUTES * 60_000;
}

function CallCard({ call, onJoin }: { call: Call; onJoin: (id: string) => void }) {
  // A meeting whose room emptied before its end has not finished (decision 23).
  const stillOn = meetingStillOn(call, new Date());
  const status = stillOn ? 'scheduled' : call.status;
  const config = statusConfig[status];
  const toast = useToast();
  const [showingLog, setShowingLog] = useState(false);

  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
          <div className="flex-1 min-w-0">
            <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1">
              <h3 className="min-w-0 break-anywhere text-lg font-semibold">{call.title || 'Untitled Call'}</h3>
              <Badge variant="secondary" className={config.color}>
                <config.icon className="mr-1 h-3 w-3" />
                {status}
              </Badge>
              <Badge variant="outline" className="text-xs">
                {call.callType === 'video' ? <Video className="mr-1 h-3 w-3" /> : <Phone className="mr-1 h-3 w-3" />}
                {call.callType}
              </Badge>
            </div>

            <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
              {call.channelName && (
                <span className="flex items-center gap-1">
                  <Users className="h-3 w-3" />
                  {call.channelName}
                </span>
              )}
              <span>
                {call.startedAt && !stillOn ? (
                  <>
                    Started {formatDate(call.startedAt)} at {formatTime(call.startedAt)}
                    {call.endedAt && (
                      <>
                        {' • '}
                        Ended {formatDate(call.endedAt)} at {formatTime(call.endedAt)}
                        {call.durationSeconds && (
                          <>
                            {' • '}
                            Duration: {Math.floor(call.durationSeconds / 60)}m {call.durationSeconds % 60}s
                          </>
                        )}
                      </>
                    )}
                  </>
                ) : (
                  <>
                    Scheduled for {formatDate(call.scheduledStart ?? call.createdAt)} at {formatTime(call.scheduledStart ?? call.createdAt)}
                  </>
                )}
              </span>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              {(call.participants ?? []).slice(0, 5).map((p) => (
                <div key={p.userId} className="relative -ml-1" title={p.userName}>
                  <div className="h-8 w-8 rounded-full bg-primary/20 flex items-center justify-center border-2 border-background">
                    <span className="text-xs font-medium text-primary">{p.userName.charAt(0).toUpperCase()}</span>
                  </div>
                </div>
              ))}
              {(call.participants ?? []).length > 5 && (
                <div className="h-8 w-8 rounded-full bg-muted flex items-center justify-center border-2 border-background">
                  <span className="text-xs font-medium text-muted-foreground">+{(call.participants ?? []).length - 5}</span>
                </div>
              )}
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2">
            <Button
              variant="ghost"
              size="icon"
              className="h-9 w-9"
              aria-label={`Copy the link to ${call.title || 'this call'}`}
              title="Copy link"
              onClick={async () => {
                if (await copyCallLink(call.id)) toast.success('Link copied. Anybody not on the call will have to ask to join.');
                else toast.error('Could not copy. Your browser did not allow it.');
              }}
            >
              <Link2 className="h-4 w-4" />
            </Button>
            {call.canReadLog !== false && (
              <Button
                variant="ghost"
                size="icon"
                className="h-9 w-9"
                aria-label={`Show the log of ${call.title || 'this call'}`}
                title="Call log"
                onClick={() => setShowingLog(true)}
              >
                <History className="h-4 w-4" />
              </Button>
            )}
            {joinable(call) ? (
              <Button variant="outline" size="sm" onClick={() => onJoin(call.id)}>
                Join
              </Button>
            ) : status === 'scheduled' ? (
              <span className="text-xs text-muted-foreground">Opens {JOIN_EARLY_MINUTES} min before</span>
            ) : null}
          </div>
        </div>
        <CallLogDialog callId={call.id} open={showingLog} onOpenChange={setShowingLog} live={call.status === 'active'} />
      </CardContent>
    </Card>
  );
}
