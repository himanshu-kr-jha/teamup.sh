import { useState } from 'react';
import { Video, Phone, Users, Search, Loader2, Clock, CheckCircle, XCircle, MoreVertical } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Card, CardContent } from '@/shared/components/ui/card';
import { Badge } from '@/shared/components/ui/badge';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { useQuery } from '@tanstack/react-query';
import { callsApi } from '@/shared/api/client';

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
  startedAt?: string;
  endedAt?: string;
  durationSeconds?: number;
  participants: { userId: string; userName: string; userAvatar?: string; role: string }[];
  createdAt: string;
}

const statusConfig = {
  scheduled: { color: 'bg-blue-100 text-blue-800', icon: Clock },
  active: { color: 'bg-green-100 text-green-800', icon: Video },
  ended: { color: 'bg-gray-100 text-gray-800', icon: CheckCircle },
  cancelled: { color: 'bg-red-100 text-red-800', icon: XCircle },
};

export function CallsPage() {
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('all');

  const { data: callsResponse, isLoading } = useQuery({
    queryKey: ['calls', { search: searchQuery, status: statusFilter }],
    queryFn: () => callsApi.list({ status: statusFilter !== 'all' ? statusFilter : undefined }),
  });

  const calls = callsResponse?.data || [];

  const filteredCalls = calls.filter((call) => {
    if (searchQuery && !call.title.toLowerCase().includes(searchQuery.toLowerCase())) return false;
    if (statusFilter !== 'all' && call.status !== statusFilter) return false;
    return true;
  });

  return (
    <div className="flex h-full flex-col bg-background">
      {/* Header */}
      <div className="flex h-16 items-center justify-between px-4 md:px-6 border-b border-border">
        <div className="flex items-center gap-4">
          <h1 className="text-2xl font-bold">Calls</h1>
        </div>
        <div className="flex items-center gap-4">
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
            className="border border-input bg-background rounded-md px-3 py-2 text-sm"
          >
            <option value="all">All Status</option>
            <option value="scheduled">Scheduled</option>
            <option value="active">Active</option>
            <option value="ended">Ended</option>
            <option value="cancelled">Cancelled</option>
          </select>
          <Button onClick={() => {}}>New Call</Button>
        </div>
      </div>

      {/* Calls List */}
      <div className="flex-1 overflow-auto p-4 md:p-6">
        {isLoading ? (
          <div className="flex h-[400px] items-center justify-center"><Loader2 className="h-8 w-8 animate-spin" /></div>
        ) : filteredCalls.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-[400px] text-muted-foreground">
            <Video className="h-12 w-12 mb-4 opacity-50" />
            <p className="text-lg">No calls found</p>
            <p className="text-sm">Create a new call or check your filters</p>
          </div>
        ) : (
          <div className="space-y-4">
            {filteredCalls.map((call) => (
              <CallCard key={call.id} call={call} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function CallCard({ call }: { call: Call }) {
  const config = statusConfig[call.status];

  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-3 mb-2">
              <h3 className="font-semibold text-lg">{call.title || 'Untitled Call'}</h3>
              <Badge variant="secondary" className={config.color}>
                <config.icon className="mr-1 h-3 w-3" />
                {call.status}
              </Badge>
              <Badge variant="outline" className="text-xs">
                {call.callType === 'video' ? <Video className="mr-1 h-3 w-3" /> : <Phone className="mr-1 h-3 w-3" />}
                {call.callType}
              </Badge>
            </div>

            <div className="flex items-center gap-4 text-sm text-muted-foreground mb-2">
              {call.channelName && (
                <span className="flex items-center gap-1">
                  <Users className="h-3 w-3" />
                  {call.channelName}
                </span>
              )}
              <span>
                {call.startedAt ? (
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
                    Scheduled for {formatDate(call.createdAt)} at {formatTime(call.createdAt)}
                  </>
                )}
              </span>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              {call.participants.slice(0, 5).map((p) => (
                <div key={p.userId} className="relative -ml-1" title={p.userName}>
                  <div className="h-8 w-8 rounded-full bg-primary/20 flex items-center justify-center border-2 border-background">
                    <span className="text-xs font-medium text-primary">{p.userName.charAt(0).toUpperCase()}</span>
                  </div>
                </div>
              ))}
              {call.participants.length > 5 && (
                <div className="h-8 w-8 rounded-full bg-muted flex items-center justify-center border-2 border-background">
                  <span className="text-xs font-medium text-muted-foreground">+{call.participants.length - 5}</span>
                </div>
              )}
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <Button variant="outline" size="sm" onClick={() => {}}>
              Join
            </Button>
            <Button variant="ghost" size="icon" onClick={() => {}}>
              <MoreVertical className="h-4 w-4" />
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
