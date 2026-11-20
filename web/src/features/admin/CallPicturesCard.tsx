import { useRef, useState, type ChangeEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ArrowRight, ImagePlus, Loader2, Trash2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Input } from '@/shared/components/ui/input';
import { orgApi, type CallBackgrounds, type CallPicture } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { CALL_BACKGROUNDS_QUERY_KEY, useCallBackgrounds } from '@/features/calls/companyBackground';
import { MAX_PICTURES, PICTURE_TYPES, nameFromFile, pictureToSend } from './callPictures';

/**
 * The organization's own background pictures for calls: the only pictures
 * anybody can put behind their camera, beside the blurs and the company name
 * templates. An administrator adds several at once, names them, puts them in
 * order and takes them away.
 */
export function CallPicturesCard() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const backgrounds = useCallBackgrounds();
  const pictures = backgrounds?.pictures ?? [];
  const fileInput = useRef<HTMLInputElement>(null);
  const [adding, setAdding] = useState<{ done: number; of: number } | null>(null);
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const full = pictures.length >= MAX_PICTURES;

  const keep = (view: CallBackgrounds) => queryClient.setQueryData(CALL_BACKGROUNDS_QUERY_KEY, view);

  const add = async (e: ChangeEvent<HTMLInputElement>) => {
    const files = [...(e.target.files ?? [])];
    e.target.value = '';
    if (!files.length) return;
    const room = MAX_PICTURES - pictures.length;
    const found: string[] = [];
    if (files.length > room) found.push(`Only ${room} more can be added: there can be at most ${MAX_PICTURES} pictures.`);
    const taken = files.slice(0, Math.max(0, room));
    setProblems([]);
    setAdding({ done: 0, of: taken.length });
    let added = 0;
    // One after another, so they are kept in the order chosen.
    for (const [i, file] of taken.entries()) {
      try {
        const picture = await pictureToSend(file);
        keep((await orgApi.addCallPicture(picture, nameFromFile(file.name))).data);
        added++;
      } catch (err) {
        found.push(`${file.name}: ${apiErrorMessage(err, 'it could not be added.')}`);
      }
      setAdding({ done: i + 1, of: taken.length });
    }
    setAdding(null);
    setProblems(found);
    if (added) toast.success(added === 1 ? 'The picture is added. Everybody can choose it in their next call.' : `${added} pictures are added. Everybody can choose them in their next call.`);
  };

  const run = async (id: string, change: () => Promise<{ data: CallBackgrounds }>, failed: string) => {
    setBusy(id);
    try {
      keep((await change()).data);
      return true;
    } catch (err) {
      toast.error(apiErrorMessage(err, failed));
      return false;
    } finally {
      setBusy(null);
    }
  };

  const move = (index: number, by: -1 | 1) => {
    const ids = pictures.map((p) => p.id);
    const [id] = ids.splice(index, 1);
    ids.splice(index + by, 0, id);
    void run(id, () => orgApi.orderCallPictures(ids), 'Could not move the picture.');
  };

  return (
    <Card data-testid="call-pictures-card">
      <CardHeader>
        <CardTitle>Background pictures</CardTitle>
        <p className="text-sm text-muted-foreground">
          The pictures everybody can choose behind their camera in calls. Nobody can use a picture of their own: only these,
          the blurs, and the company backgrounds below.
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        {!backgrounds ? (
          <div className="flex h-32 items-center justify-center rounded-lg border border-border bg-muted/40">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" aria-label="Loading" />
          </div>
        ) : pictures.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border bg-muted/30 px-4 py-8 text-center">
            <ImagePlus className="h-6 w-6 text-muted-foreground" aria-hidden="true" />
            <p className="text-sm font-medium">No background pictures yet</p>
            <p className="max-w-sm text-sm text-muted-foreground">
              Add a few, and they are the backgrounds everybody in the organization can choose from.
            </p>
          </div>
        ) : (
          <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4" aria-label="Background pictures">
            {pictures.map((p, i) => (
              <PictureItem
                key={p.id}
                picture={p}
                busy={busy === p.id}
                first={i === 0}
                last={i === pictures.length - 1}
                onMove={(by) => move(i, by)}
                onRename={(name) => run(p.id, () => orgApi.renameCallPicture(p.id, name), 'Could not rename the picture.')}
                onRemove={async () => {
                  if (await run(p.id, () => orgApi.removeCallPicture(p.id), 'Could not remove the picture.')) {
                    toast.success(`${p.name} is removed. Anybody who had it on goes back to no background.`);
                  }
                }}
              />
            ))}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <input
            ref={fileInput}
            type="file"
            multiple
            accept={PICTURE_TYPES.join(',')}
            className="hidden"
            onChange={add}
            aria-label="Background picture files"
          />
          <Button type="button" variant="outline" size="sm" disabled={!backgrounds || !!adding || full} onClick={() => fileInput.current?.click()}>
            {adding ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <ImagePlus className="mr-2 h-4 w-4" />}
            {adding ? `Adding ${Math.min(adding.done + 1, adding.of)} of ${adding.of}…` : 'Add pictures'}
          </Button>
          <span className="text-xs text-muted-foreground">
            {full
              ? `That is the most there can be: remove one to add another.`
              : `${pictures.length} of ${MAX_PICTURES}. PNG, JPEG or WebP, at least 1280 x 720. Each is cut to 16:9 from its middle.`}
          </span>
        </div>
        {problems.length > 0 && (
          <ul className="space-y-1 text-sm text-destructive" role="alert">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function PictureItem({
  picture,
  busy,
  first,
  last,
  onMove,
  onRename,
  onRemove,
}: {
  picture: CallPicture;
  busy: boolean;
  first: boolean;
  last: boolean;
  onMove: (by: -1 | 1) => void;
  onRename: (name: string) => Promise<boolean>;
  onRemove: () => void;
}) {
  const [name, setName] = useState(picture.name);
  const [editedFrom, setEditedFrom] = useState(picture.name);
  // A new name from the server, such as after a rename, is shown.
  if (editedFrom !== picture.name) {
    setEditedFrom(picture.name);
    setName(picture.name);
  }
  const save = async () => {
    const next = name.trim();
    if (!next || next === picture.name) {
      setName(picture.name);
      return;
    }
    if (!(await onRename(next))) setName(picture.name);
  };
  return (
    <li className="min-w-0 space-y-2" aria-label={picture.name}>
      <div className="relative aspect-video overflow-hidden rounded-lg border border-border bg-muted/40">
        <img src={picture.thumbUrl} alt={picture.name} className="h-full w-full object-cover" draggable={false} />
        {busy && (
          <span className="absolute inset-0 flex items-center justify-center bg-background/60">
            <Loader2 className="h-5 w-5 animate-spin" aria-label="Saving" />
          </span>
        )}
      </div>
      <div className="flex items-center gap-1">
        <Input
          value={name}
          maxLength={60}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => void save()}
          onKeyDown={(e) => {
            if (e.key === 'Enter') e.currentTarget.blur();
            if (e.key === 'Escape') {
              setName(picture.name);
              e.currentTarget.blur();
            }
          }}
          aria-label={`Name of ${picture.name}`}
          className="h-8 min-w-0 flex-1 text-sm"
          disabled={busy}
        />
        <Button type="button" variant="ghost" size="icon" className="h-8 w-8 shrink-0" disabled={busy || first} onClick={() => onMove(-1)} aria-label={`Move ${picture.name} earlier`} title="Move earlier">
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <Button type="button" variant="ghost" size="icon" className="h-8 w-8 shrink-0" disabled={busy || last} onClick={() => onMove(1)} aria-label={`Move ${picture.name} later`} title="Move later">
          <ArrowRight className="h-4 w-4" />
        </Button>
        <Button type="button" variant="ghost" size="icon" className="h-8 w-8 shrink-0 text-muted-foreground hover:text-destructive" disabled={busy} onClick={onRemove} aria-label={`Remove ${picture.name}`} title="Remove">
          <Trash2 className="h-4 w-4" />
        </Button>
      </div>
    </li>
  );
}
