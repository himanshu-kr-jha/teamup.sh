import { useEffect, useRef, useState, type ChangeEvent, type CSSProperties } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Check, Hash, ImagePlus, Loader2, MessageSquare, RotateCcw, Trash2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { BRAND_PRESETS, DEFAULT_BRAND_HEX, isHexColor, textOn, tokensFor } from '@/shared/branding';
import { cn } from '@/shared/utils/cn';
import { BRANDING_QUERY_KEY, useBranding } from '@/features/branding/useBranding';

/** The largest logo the server takes. */
const MAX_LOGO_BYTES = 512 * 1024;
const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'];

/**
 * The organization's colour and logo, for everybody, including the sign-in page.
 *
 * Nothing changes for anybody until it is saved; the preview beside the choices
 * shows what it will look like first. Leaving both alone is TeamUp's own look.
 */
export function AppearanceTab() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const branding = useBranding();
  const fileInput = useRef<HTMLInputElement>(null);

  const [color, setColor] = useState('');
  const [hexText, setHexText] = useState('');
  /** undefined: as saved. null: removed. A string: a new picture, as a data URL. */
  const [logo, setLogo] = useState<string | null | undefined>(undefined);
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState('');

  useEffect(() => {
    if (!branding) return;
    setColor(branding.color);
    setHexText(branding.color);
    setLogo(undefined);
  }, [branding]);

  if (!branding) {
    return <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" aria-label="Loading" />;
  }

  const shownLogo = logo === undefined ? branding.logoUrl : logo ?? '';
  const dirty = color !== branding.color || logo !== undefined;
  const isDefault = color === '' && !shownLogo;

  const pick = (next: string) => {
    setColor(next);
    setHexText(next);
    setProblem('');
  };

  const typeHex = (e: ChangeEvent<HTMLInputElement>) => {
    let value = e.target.value.trim();
    if (value && !value.startsWith('#')) value = `#${value}`;
    setHexText(value);
    if (value === '') setColor('');
    else if (isHexColor(value)) setColor(value.toLowerCase());
  };

  const chooseLogo = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (!LOGO_TYPES.includes(file.type)) {
      setProblem('The logo must be a PNG, JPEG, WebP or GIF picture.');
      return;
    }
    if (file.size > MAX_LOGO_BYTES) {
      setProblem('The logo must be 512 KB or smaller.');
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      setLogo(String(reader.result));
      setProblem('');
    };
    reader.onerror = () => setProblem('That picture could not be read.');
    reader.readAsDataURL(file);
  };

  const save = async () => {
    setSaving(true);
    setProblem('');
    try {
      const res = await orgApi.saveBranding({ color, ...(logo === undefined ? {} : { logo: logo ?? '' }) });
      queryClient.setQueryData(BRANDING_QUERY_KEY, res.data);
      toast.success('Appearance saved. Everybody sees it the next time they look.');
    } catch (err) {
      setProblem(apiErrorMessage(err, 'Could not save the appearance.'));
    } finally {
      setSaving(false);
    }
  };

  const tokens = tokensFor(color);
  const previewStyle = (tokens
    ? { '--primary': tokens.primary, '--ring': tokens.primary, '--primary-foreground': tokens.primaryForeground }
    : {}) as CSSProperties;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <p className="text-sm text-muted-foreground">
          Your organization's colour and logo, for everybody, on every page including sign-in.
        </p>
      </CardHeader>
      <CardContent>
        <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_18rem]">
          <div className="min-w-0 space-y-6">
            <fieldset className="space-y-3">
              <legend className="text-sm font-medium">Colour</legend>
              <div className="flex flex-wrap gap-2" role="radiogroup" aria-label="Colour">
                {BRAND_PRESETS.map((preset) => {
                  const selected = color === preset.color;
                  return (
                    <button
                      key={preset.name}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      aria-label={preset.name}
                      title={preset.name}
                      onClick={() => pick(preset.color)}
                      className={cn(
                        'flex h-9 w-9 items-center justify-center rounded-full ring-offset-2 ring-offset-background transition-shadow focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                        selected && 'ring-2 ring-foreground'
                      )}
                      style={{ backgroundColor: preset.color || DEFAULT_BRAND_HEX }}
                    >
                      {selected && (
                        <Check
                          className={cn(
                            'h-4 w-4',
                            textOn(preset.color || DEFAULT_BRAND_HEX) === 'light' ? 'text-white' : 'text-slate-900'
                          )}
                          aria-hidden="true"
                        />
                      )}
                    </button>
                  );
                })}
              </div>
              <div className="flex max-w-xs items-end gap-2">
                <div className="min-w-0 flex-1 space-y-2">
                  <Label htmlFor="brandHex">Or any colour</Label>
                  <Input
                    id="brandHex"
                    value={hexText}
                    onChange={typeHex}
                    placeholder={DEFAULT_BRAND_HEX}
                    autoComplete="off"
                    spellCheck={false}
                    aria-invalid={hexText !== '' && !isHexColor(hexText)}
                  />
                </div>
                <input
                  type="color"
                  aria-label="Choose a colour"
                  value={color || DEFAULT_BRAND_HEX}
                  onChange={(e) => pick(e.target.value)}
                  className="h-10 w-12 shrink-0 cursor-pointer rounded-md border border-input bg-background p-1"
                />
              </div>
              {hexText !== '' && !isHexColor(hexText) && (
                <p className="text-xs text-destructive">A colour looks like #4f46e5.</p>
              )}
            </fieldset>

            <fieldset className="space-y-3">
              <legend className="text-sm font-medium">Logo</legend>
              <p className="text-xs text-muted-foreground">
                A PNG, JPEG, WebP or GIF up to 512 KB. It is shown about 32 pixels high, so a wide logo on a
                transparent background works best.
              </p>
              <div className="flex flex-wrap items-center gap-3">
                {shownLogo ? (
                  <img
                    src={shownLogo}
                    alt="Logo"
                    className="h-12 max-w-[12rem] rounded border border-border bg-muted/40 object-contain p-1"
                  />
                ) : (
                  <span className="flex h-12 w-12 items-center justify-center rounded border border-dashed border-border text-muted-foreground">
                    <Hash className="h-6 w-6" aria-hidden="true" />
                  </span>
                )}
                <input
                  ref={fileInput}
                  type="file"
                  accept={LOGO_TYPES.join(',')}
                  className="hidden"
                  onChange={chooseLogo}
                  aria-label="Logo file"
                />
                <Button type="button" variant="outline" size="sm" onClick={() => fileInput.current?.click()}>
                  <ImagePlus className="mr-2 h-4 w-4" />
                  {shownLogo ? 'Change logo' : 'Upload logo'}
                </Button>
                {shownLogo && (
                  <Button type="button" variant="ghost" size="sm" onClick={() => setLogo(null)}>
                    <Trash2 className="mr-2 h-4 w-4" />
                    Remove
                  </Button>
                )}
              </div>
            </fieldset>

            {problem && (
              <p className="text-sm text-destructive" role="alert">
                {problem}
              </p>
            )}

            <div className="flex flex-wrap gap-2">
              <Button onClick={save} disabled={!dirty || saving || (hexText !== '' && !isHexColor(hexText))}>
                {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                Save
              </Button>
              <Button
                variant="outline"
                disabled={isDefault}
                onClick={() => {
                  pick('');
                  setLogo(branding.logoUrl ? null : undefined);
                }}
              >
                <RotateCcw className="mr-2 h-4 w-4" />
                Back to TeamUp's look
              </Button>
              {dirty && (
                <Button
                  variant="ghost"
                  onClick={() => {
                    pick(branding.color);
                    setLogo(undefined);
                  }}
                >
                  Undo changes
                </Button>
              )}
            </div>
          </div>

          {/* The choices on a small piece of the app, before anybody else sees them. */}
          <div style={previewStyle} aria-label="Preview" role="group" className="min-w-0">
            <p className="mb-2 text-sm font-medium">Preview</p>
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              <div className="flex h-12 items-center gap-2 border-b border-border px-3">
                {shownLogo ? (
                  <img src={shownLogo} alt="" className="h-7 max-w-[7rem] object-contain" />
                ) : (
                  <Hash className="h-7 w-7 text-primary" aria-hidden="true" />
                )}
                <span className="truncate font-bold">{shownLogo ? branding.name : 'TeamUp'}</span>
              </div>
              <div className="space-y-3 p-3">
                <div className="flex items-center gap-3 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground">
                  <MessageSquare className="h-4 w-4" aria-hidden="true" />
                  <span className="flex-1">Chat</span>
                  <span className="rounded-full bg-primary-foreground/20 px-1.5 text-xs">3</span>
                </div>
                <div className="flex items-center gap-3 px-3 py-1 text-sm text-muted-foreground">
                  <Hash className="h-4 w-4" aria-hidden="true" />
                  <span className="flex-1">general</span>
                  <span className="rounded-full bg-primary px-1.5 text-xs font-semibold text-primary-foreground">2</span>
                </div>
                <Button size="sm" className="w-full" tabIndex={-1}>
                  Send
                </Button>
              </div>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
