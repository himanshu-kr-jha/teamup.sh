import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, FileText, Hash, Loader2, Megaphone, Search, Send, Share2, X } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { channelsApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { useAuth } from '@/features/auth/AuthProvider';
import { PERMS, can } from '@/shared/permissions';
import { cn } from '@/shared/utils/cn';
import { FormattedTextarea } from '@/shared/richText/composer';
import { uploadFile } from '@/features/files/useUpload';
import { humanSize } from '@/features/files/AttachmentChip';
import { MAX_FORWARD_TARGETS } from '@/features/chat/ForwardDialog';
import { SHARE_MAX_BYTES, SHARE_MAX_FILES, clearInbox, composeShared, readInbox } from './inbox';

interface Conversation {
  id: string;
  name?: string;
  type: string;
  counterpartId?: string;
  notifyLevel?: string | null;
  lastMessageAt?: string;
  createdAt?: string;
}

interface Person {
  id: string;
  name?: string;
  email?: string;
  avatarUrl?: string | null;
  isActive?: boolean;
}

/** Somebody to send to: a conversation you are in, or a person you have not talked to yet. */
type Target = { kind: 'conversation'; id: string; name: string } | { kind: 'person'; id: string; name: string };

const keyOf = (t: Target) => `${t.kind}:${t.id}`;

/**
 * Where something shared from another app lands: what came, which can be
 * changed before it goes, and whom to send it to, from the conversations in
 * use or anybody in the organization in a new chat.
 */
export function SharePage() {
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [params] = useSearchParams();
  const isAdmin = can(user, PERMS.channelsAll);

  const [loaded, setLoaded] = useState(false);
  const [text, setText] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [leftOut, setLeftOut] = useState(0);
  const [fromLink, setFromLink] = useState(false);
  const [query, setQuery] = useState('');
  const [chosen, setChosen] = useState<Target[]>([]);
  const [status, setStatus] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    void readInbox().then((shared) => {
      if (!live) return;
      // A link to /share?text=… shares too, from wherever can only make a link.
      const fromLink = composeShared({
        title: params.get('title') ?? '',
        text: params.get('text') ?? '',
        url: params.get('url') ?? '',
      });
      setText(shared?.text || fromLink);
      setFiles(shared?.files ?? []);
      setLeftOut(shared?.leftOut ?? 0);
      // Any page on the web can make such a link, so its words are not to be taken
      // as the person's own without a look.
      setFromLink(!shared?.text && !!fromLink);
      setLoaded(true);
    });
    return () => {
      live = false;
    };
    // Read once, when the page opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const { data: channelData, isLoading: loadingChannels } = useQuery({ queryKey: ['channels'], queryFn: () => channelsApi.list() });
  const { data: userData } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });

  const term = query.trim().toLowerCase();
  const matches = (...words: Array<string | undefined>) => !term || words.some((w) => (w ?? '').toLowerCase().includes(term));

  const conversations = useMemo(() => {
    const all = (channelData?.data ?? []) as Conversation[];
    return (
      all
        // Yours to post in: ones you have joined, and announcements only for those who make them.
        .filter((c) => c.notifyLevel != null && (c.type !== 'announcement' || isAdmin))
        .sort((a, b) => (b.lastMessageAt ?? b.createdAt ?? '').localeCompare(a.lastMessageAt ?? a.createdAt ?? ''))
    );
  }, [channelData, isAdmin]);

  const people = useMemo(() => {
    const talkedTo = new Set(conversations.filter((c) => c.type === 'dm').map((c) => c.counterpartId));
    return ((userData?.data ?? []) as Person[])
      .filter((p) => p.id !== user?.id && p.isActive !== false && !talkedTo.has(p.id))
      .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? ''));
  }, [userData, conversations, user?.id]);

  const shownConversations = conversations.filter((c) => matches(c.name));
  const shownPeople = people.filter((p) => matches(p.name, p.email));

  const isChosen = (t: Target) => chosen.some((c) => keyOf(c) === keyOf(t));
  const toggle = (t: Target) =>
    setChosen((now) =>
      now.some((c) => keyOf(c) === keyOf(t))
        ? now.filter((c) => keyOf(c) !== keyOf(t))
        : now.length >= MAX_FORWARD_TARGETS
          ? now
          : [...now, t]
    );

  const nothing = !text.trim() && files.length === 0;

  const putAside = async () => {
    await clearInbox();
    navigate('/chat');
  };

  const send = async () => {
    if (nothing || chosen.length === 0) return;
    const sentTo: string[] = [];
    try {
      for (const target of chosen) {
        const label = chosen.length > 1 ? ` to ${target.name}` : '';
        setStatus(`Sending${label}…`);
        const channelId =
          target.kind === 'conversation' ? target.id : (await channelsApi.openDirect(target.id)).data.id;
        const attachmentIds: string[] = [];
        for (const [i, file] of files.entries()) {
          const of = files.length > 1 ? ` ${i + 1} of ${files.length}` : '';
          const uploaded = await uploadFile('chat', channelId, file, (pct) => setStatus(`Uploading${of}${label}: ${pct}%`));
          attachmentIds.push(uploaded.id);
        }
        setStatus(`Sending${label}…`);
        await channelsApi.sendMessage(channelId, {
          content: text.trim(),
          tempId: `temp-${Date.now()}-${sentTo.length}`,
          attachmentIds: attachmentIds.length ? attachmentIds : undefined,
        });
        sentTo.push(channelId);
      }
      await clearInbox();
      void queryClient.invalidateQueries({ queryKey: ['channels'] });
      for (const id of sentTo) void queryClient.invalidateQueries({ queryKey: ['messages', id] });
      toast.success(sentTo.length === 1 ? 'Sent.' : `Sent to ${sentTo.length} conversations.`);
      navigate(sentTo.length === 1 ? `/chat/${sentTo[0]}` : '/chat');
    } catch (err) {
      // What went already stays sent; only the rest is left to choose again.
      if (sentTo.length) {
        setChosen((now) => now.slice(sentTo.length));
        toast.error(`${apiErrorMessage(err, 'Could not send it.')} It went to ${sentTo.length} of them.`);
      } else {
        toast.error(apiErrorMessage(err, 'Could not send it.'));
      }
    } finally {
      setStatus(null);
    }
  };

  const busy = status !== null;

  return (
    <div className="flex h-full flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-2xl space-y-5 px-4 py-6 md:px-6 md:py-8">
          <header>
            <p className="flex items-center gap-1.5 text-sm font-medium text-primary">
              <Share2 className="h-4 w-4" aria-hidden="true" />
              Share
            </p>
            <h1 className="mt-1 text-2xl font-bold">Send to TeamUp</h1>
            <p className="mt-1 text-sm text-muted-foreground">Change it if you like, then choose whom it goes to.</p>
          </header>

          <section aria-label="What you are sharing" className="space-y-2">
            <FormattedTextarea
              value={text}
              onValueChange={setText}
              placeholder={loaded ? 'Add a message' : 'Loading what you shared…'}
              aria-label="Message"
              maxHeight={220}
              disabled={busy}
            />
            {files.length > 0 && (
              <ul className="flex flex-wrap gap-2" aria-label="Files">
                {files.map((f, i) => (
                  <SharedFile key={i} file={f} disabled={busy} onRemove={() => setFiles((all) => all.filter((_, j) => j !== i))} />
                ))}
              </ul>
            )}
            {leftOut > 0 && (
              <p className="text-sm text-muted-foreground" role="status">
                {leftOut === 1 ? '1 file was' : `${leftOut} files were`} left out: a share can carry {SHARE_MAX_FILES} files
                and {SHARE_MAX_BYTES / 1024 / 1024} MB at most.
              </p>
            )}
            {fromLink && (
              <p className="text-sm text-muted-foreground" role="status">
                This message came in a link from outside TeamUp. Read it before you send it.
              </p>
            )}
            {loaded && nothing && (
              <p className="text-sm text-muted-foreground">
                Nothing came with the share. Write a message to send, or use Share in another app and pick TeamUp.
              </p>
            )}
          </section>

          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" aria-hidden="true" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Find a conversation or a person"
              className="pl-8"
              aria-label="Find a conversation or a person"
            />
          </div>

          <section aria-labelledby="share-conversations">
            <h2 id="share-conversations" className="mb-1 px-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Recent conversations
            </h2>
            <ul className="-mx-1" role="listbox" aria-multiselectable="true" aria-labelledby="share-conversations">
              {loadingChannels && <li className="px-3 py-4 text-sm text-muted-foreground">Loading…</li>}
              {!loadingChannels && shownConversations.length === 0 && (
                <li className="px-3 py-4 text-sm text-muted-foreground">No conversations match.</li>
              )}
              {shownConversations.map((c) => {
                const t: Target = { kind: 'conversation', id: c.id, name: c.name || 'Unnamed' };
                return (
                  <Choice
                    key={c.id}
                    picked={isChosen(t)}
                    full={!isChosen(t) && chosen.length >= MAX_FORWARD_TARGETS}
                    onToggle={() => toggle(t)}
                    icon={
                      c.type === 'dm' ? (
                        <PersonAvatar userId={c.counterpartId} name={c.name} className="h-8 w-8" decorative />
                      ) : (
                        <span className="flex h-8 w-8 items-center justify-center rounded-full bg-muted">
                          {c.type === 'announcement' ? (
                            <Megaphone className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
                          ) : (
                            <Hash className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
                          )}
                        </span>
                      )
                    }
                    label={t.name}
                  />
                );
              })}
            </ul>
          </section>

          {shownPeople.length > 0 && (
            <section aria-labelledby="share-people">
              <h2 id="share-people" className="mb-1 px-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                New chat
              </h2>
              <ul className="-mx-1" role="listbox" aria-multiselectable="true" aria-labelledby="share-people">
                {shownPeople.map((p) => {
                  const t: Target = { kind: 'person', id: p.id, name: p.name || p.email || 'Somebody' };
                  return (
                    <Choice
                      key={p.id}
                      picked={isChosen(t)}
                      full={!isChosen(t) && chosen.length >= MAX_FORWARD_TARGETS}
                      onToggle={() => toggle(t)}
                      icon={<PersonAvatar userId={p.id} name={p.name} src={p.avatarUrl} className="h-8 w-8" decorative />}
                      label={t.name}
                      detail={p.email}
                    />
                  );
                })}
              </ul>
            </section>
          )}
        </div>
      </div>

      <footer className="border-t border-border bg-background">
        <div className="mx-auto flex max-w-2xl items-center gap-2 px-4 py-3 md:px-6">
          <span className="mr-auto min-w-0 truncate text-sm text-muted-foreground" aria-live="polite">
            {status ??
              (chosen.length === 0
                ? 'Choose whom to send it to'
                : chosen.length >= MAX_FORWARD_TARGETS
                  ? `${MAX_FORWARD_TARGETS} at most`
                  : chosen.map((c) => c.name).join(', '))}
          </span>
          <Button variant="outline" onClick={() => void putAside()} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void send()} disabled={busy || nothing || chosen.length === 0}>
            {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Send className="mr-2 h-4 w-4" />}
            Send
          </Button>
        </div>
      </footer>
    </div>
  );
}

function Choice({
  picked,
  full,
  onToggle,
  icon,
  label,
  detail,
}: {
  picked: boolean;
  full: boolean;
  onToggle: () => void;
  icon: React.ReactNode;
  label: string;
  detail?: string;
}) {
  return (
    <li>
      <button
        type="button"
        role="option"
        aria-selected={picked}
        disabled={full}
        onClick={onToggle}
        className={cn(
          'flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors',
          picked ? 'bg-primary/10' : 'hover:bg-accent',
          full && 'opacity-50'
        )}
      >
        {icon}
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{label}</span>
          {detail && <span className="block truncate text-xs text-muted-foreground">{detail}</span>}
        </span>
        <span
          className={cn(
            'flex h-5 w-5 shrink-0 items-center justify-center rounded-full border',
            picked ? 'border-primary bg-primary text-primary-foreground' : 'border-input'
          )}
          aria-hidden="true"
        >
          {picked && <Check className="h-3.5 w-3.5" />}
        </span>
      </button>
    </li>
  );
}

function SharedFile({ file, onRemove, disabled }: { file: File; onRemove: () => void; disabled: boolean }) {
  const image = file.type.startsWith('image/');
  const [preview, setPreview] = useState<string | null>(null);
  useEffect(() => {
    if (!image) return;
    const url = URL.createObjectURL(file);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [file, image]);

  return (
    <li className="relative flex max-w-full items-center gap-2 rounded-md border border-border bg-muted/40 p-1.5 pr-8 text-sm">
      {preview ? (
        <img src={preview} alt="" className="h-12 w-12 rounded object-cover" />
      ) : (
        <span className="flex h-12 w-12 items-center justify-center rounded bg-background">
          <FileText className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
        </span>
      )}
      <span className="min-w-0">
        <span className="block max-w-[12rem] truncate font-medium">{file.name}</span>
        <span className="block text-xs text-muted-foreground">{humanSize(file.size)}</span>
      </span>
      <button
        type="button"
        onClick={onRemove}
        disabled={disabled}
        aria-label={`Remove ${file.name}`}
        className="absolute right-1 top-1 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        <X className="h-4 w-4" aria-hidden="true" />
      </button>
    </li>
  );
}
