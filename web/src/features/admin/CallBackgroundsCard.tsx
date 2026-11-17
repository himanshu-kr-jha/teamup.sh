import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ImagePlus, Loader2, Trash2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuthStore } from '@/shared/store/authStore';
import {
  CALL_BACKGROUNDS_QUERY_KEY,
  composeCompanyBackground,
  useCompanyTemplates,
  type CompanyTheme,
} from '@/features/calls/companyBackground';

/** The most the server takes, which is also the most any request may carry. */
const MAX_BYTES = 2 * 1024 * 1024;
const TYPES = ['image/png', 'image/jpeg', 'image/webp'];

const THEMES: { theme: CompanyTheme; label: string }[] = [
  { theme: 'dark', label: 'Dark' },
  { theme: 'light', label: 'Light' },
];

/**
 * The organization's call backgrounds: a template for a dark look and one for a
 * light one. Everybody can then choose one behind their camera in a call, and
 * their own name and company role is written in its top right corner.
 */
export function CallBackgroundsCard() {
  const templates = useCompanyTemplates();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Call backgrounds</CardTitle>
        <p className="text-sm text-muted-foreground">
          A company background for calls, in a dark and a light version. Everybody can choose it behind their camera, and
          their own name and company role are written in the top right corner, so leave that corner empty.
        </p>
      </CardHeader>
      <CardContent>
        <div className="grid max-w-5xl gap-6 md:grid-cols-2">
          {THEMES.map((t) => (
            <Template key={t.theme} theme={t.theme} label={t.label} url={templates?.[t.theme] ?? ''} loading={!templates} />
          ))}
        </div>
        <p className="mt-4 text-xs text-muted-foreground">
          A 16:9 PNG, JPEG or WebP picture, at least 1280 x 720 and up to 2 MB. The preview shows your own name; everybody
          sees theirs.
        </p>
      </CardContent>
    </Card>
  );
}

function Template({ theme, label, url, loading }: { theme: CompanyTheme; label: string; url: string; loading: boolean }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState('');

  const upload = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (!TYPES.includes(file.type)) {
      setProblem('The background must be a PNG, JPEG or WebP picture.');
      return;
    }
    if (file.size > MAX_BYTES) {
      setProblem('That picture is too large: the most is 2 MB.');
      return;
    }
    setBusy(true);
    setProblem('');
    try {
      // The shape is checked here too, so a wrong one is said at once; the
      // server checks again whatever is sent.
      const shapeProblem = await checkShape(file);
      if (shapeProblem) {
        setProblem(shapeProblem);
        return;
      }
      const res = await orgApi.setCallBackground(theme, file);
      queryClient.setQueryData(CALL_BACKGROUNDS_QUERY_KEY, res.data);
      toast.success(`The ${label.toLowerCase()} call background is saved. Everybody can choose it in their next call.`);
    } catch (err) {
      setProblem(apiErrorMessage(err, 'Could not save the background.'));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    setProblem('');
    try {
      const res = await orgApi.removeCallBackground(theme);
      queryClient.setQueryData(CALL_BACKGROUNDS_QUERY_KEY, res.data);
      toast.success(`The ${label.toLowerCase()} call background is removed.`);
    } catch (err) {
      setProblem(apiErrorMessage(err, 'Could not remove the background.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="min-w-0 space-y-3" aria-label={`${label} call background`}>
      <h3 className="text-sm font-medium">{label}</h3>
      <div className="relative aspect-video overflow-hidden rounded-lg border border-border bg-muted/40">
        {url ? (
          <Preview theme={theme} url={url} />
        ) : (
          <span className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
            {loading ? <Loader2 className="h-5 w-5 animate-spin" aria-label="Loading" /> : `No ${label.toLowerCase()} background`}
          </span>
        )}
        {busy && (
          <span className="absolute inset-0 flex items-center justify-center bg-background/60">
            <Loader2 className="h-6 w-6 animate-spin" aria-label="Saving" />
          </span>
        )}
      </div>
      <div className="flex flex-wrap gap-2">
        <input
          ref={fileInput}
          type="file"
          accept={TYPES.join(',')}
          className="hidden"
          onChange={upload}
          aria-label={`${label} background file`}
        />
        <Button type="button" variant="outline" size="sm" disabled={busy || loading} onClick={() => fileInput.current?.click()}>
          <ImagePlus className="mr-2 h-4 w-4" />
          {url ? 'Replace' : 'Upload'}
        </Button>
        {url && (
          <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={remove}>
            <Trash2 className="mr-2 h-4 w-4" />
            Remove
          </Button>
        )}
      </div>
      {problem && (
        <p className="text-sm text-destructive" role="alert">
          {problem}
        </p>
      )}
    </section>
  );
}

/** Why a picture will not do as a background, by its size; empty when it will. */
async function checkShape(file: File): Promise<string> {
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    const { naturalWidth: w, naturalHeight: h } = img;
    // 16:9, allowing for a pixel or so of rounding, as the server does.
    if (Math.abs(w * 9 - h * 16) > w / 100) return 'The background must be 16:9, the shape of a video, such as 1920 x 1080.';
    if (w < 1280) return 'The background must be at least 1280 x 720.';
    return '';
  } catch {
    return 'That picture could not be read.';
  } finally {
    URL.revokeObjectURL(url);
  }
}

/** The template as a person will have it, with the administrator's own name on. */
function Preview({ theme, url }: { theme: CompanyTheme; url: string }) {
  const name = useAuthStore((s) => s.user?.name ?? '');
  const role = useAuthStore((s) => s.user?.jobTitle ?? '');
  const [picture, setPicture] = useState<{ url: string; src: string } | null>(null);
  useEffect(() => {
    let live = true;
    composeCompanyBackground({ template: url, theme, name, role }, 1280, 720)
      .then((src) => live && setPicture({ url, src }))
      .catch(() => live && setPicture({ url, src: url }));
    return () => {
      live = false;
    };
  }, [url, theme, name, role]);
  // The plain template until the name is on it, rather than an empty box.
  return (
    <img
      src={picture?.url === url ? picture.src : url}
      alt={`The ${theme} call background`}
      className="h-full w-full object-cover"
      draggable={false}
    />
  );
}
