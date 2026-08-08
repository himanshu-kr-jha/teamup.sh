import { useParams } from 'react-router-dom';
import { Button } from '@/shared/components/ui/button';
import { cn } from '@/shared/utils/cn';
import { Mic, Camera, Monitor, X, Loader2 } from 'lucide-react';

export function CallViewPage() {
  const { roomName } = useParams<{ roomName: string }>();

  return (
    <div className="flex h-screen w-full flex-col bg-background">
      <div className="flex h-16 items-center justify-between px-4 border-b border-border bg-background/95 backdrop-blur">
        <div className="flex items-center gap-4">
          <h1 className="text-lg font-semibold">Call: {roomName}</h1>
          <span className={cn('px-2 py-1 rounded-full text-xs font-medium', 'bg-yellow-100 text-yellow-800')}>
            LiveKit integration - see CallViewPage.tsx for full implementation
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon" disabled>
            <Monitor className="h-5 w-5" />
          </Button>
          <Button variant="ghost" size="icon" className="text-destructive">
            <X className="h-5 w-5" />
          </Button>
        </div>
      </div>

      <div className="flex-1 overflow-hidden relative">
        <div className="flex h-full w-full items-center justify-center bg-muted/50">
          <div className="text-center text-muted-foreground">
            <Loader2 className="h-8 w-8 animate-spin mx-auto mb-4 text-primary" />
            <p className="text-lg font-medium">LiveKit Call Integration</p>
            <p className="text-sm">Room: {roomName}</p>
            <p className="text-sm mt-2">Full implementation in CallViewPage.tsx with @livekit/components-react</p>
          </div>
        </div>

        <div className="flex h-20 items-center justify-center gap-4 px-4 border-t border-border bg-background/95 backdrop-blur">
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="icon" disabled>
              <Mic className="h-5 w-5" />
            </Button>
            <Button variant="secondary" size="icon" disabled>
              <Camera className="h-5 w-5" />
            </Button>
            <Button variant="outline" size="icon" disabled>
              <Monitor className="h-5 w-5" />
            </Button>
          </div>
          <div className="flex-1" />
          <Button variant="destructive" size="icon" className="h-12 w-12 rounded-full">
            <X className="h-6 w-6" />
          </Button>
        </div>
      </div>
    </div>
  );
}
