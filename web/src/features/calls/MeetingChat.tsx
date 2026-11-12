import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useRoomContext } from '@livekit/components-react';
import { RoomEvent, type RemoteParticipant } from 'livekit-client';
import { MessageSquareText, SendHorizontal, X } from 'lucide-react';
import type { MeetingChatMessage } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { Button } from '@/shared/components/ui/button';
import { LinkedText } from '@/shared/components/LinkedText';
import { useToast } from '@/shared/hooks/useToast';
import { cn, formatDate, formatTime } from '@/shared/utils/cn';
import { MAX_CHAT_MESSAGE, chatFromPacket, mergeChat, startsGroup, unreadCount } from './meetingChat';

/**
 * A meeting's own chat, beside the video, as in a video meeting's side panel:
 * everybody in the room, guests too, writes and reads it. The server keeps each
 * message and sends it to the room; the page reads what came before on opening,
 * and again after losing its connection, so nothing is missed.
 */

/** How the page reaches the chat: a member through their session, a guest with their key. */
export interface ChatTransport {
  load: () => Promise<MeetingChatMessage[]>;
  send: (content: string) => Promise<MeetingChatMessage>;
}

interface ChatState {
  messages: MeetingChatMessage[];
  loaded: boolean;
  me: string;
  open: boolean;
  setOpen: (open: boolean) => void;
  unread: number;
  preview: MeetingChatMessage | null;
  dismissPreview: () => void;
  send: (content: string) => Promise<void>;
}

const ChatContext = createContext<ChatState | null>(null);

/** How long a message from somebody else shows over the video while the chat is closed. */
const PREVIEW_FOR = 6_000;

export function MeetingChatProvider({ transport, children }: { transport: ChatTransport; children: ReactNode }) {
  const room = useRoomContext();
  const [messages, setMessages] = useState<MeetingChatMessage[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [open, setOpenState] = useState(false);
  const [seen, setSeen] = useState<ReadonlySet<string>>(() => new Set());
  const [preview, setPreview] = useState<MeetingChatMessage | null>(null);
  const [me, setMe] = useState(room.localParticipant.identity);

  const transportRef = useRef(transport);
  transportRef.current = transport;
  const openRef = useRef(open);
  openRef.current = open;
  const firstLoad = useRef(true);

  const markSeen = useCallback((list: MeetingChatMessage[]) => {
    setSeen((prev) => {
      if (list.every((m) => prev.has(m.id))) return prev;
      const next = new Set(prev);
      for (const m of list) next.add(m.id);
      return next;
    });
  }, []);

  const load = useCallback(async () => {
    try {
      const list = await transportRef.current.load();
      setMessages((known) => mergeChat(known, list));
      // What was written before you came in is there to read, not news.
      if (firstLoad.current) markSeen(list);
      firstLoad.current = false;
      setLoaded(true);
    } catch {
      // The panel says it could not load; the next reconnect tries again.
      setLoaded(true);
    }
  }, [markSeen]);

  useEffect(() => {
    void load();
    const onConnected = () => setMe(room.localParticipant.identity);
    const onReconnected = () => void load();
    const onData = (payload: Uint8Array, from?: RemoteParticipant, _kind?: unknown, topic?: string) => {
      const m = chatFromPacket(payload, from, topic);
      if (!m) return;
      setMessages((known) => mergeChat(known, [m]));
      if (openRef.current) markSeen([m]);
      else if (m.senderId !== room.localParticipant.identity) setPreview(m);
    };
    room.on(RoomEvent.Connected, onConnected);
    room.on(RoomEvent.Reconnected, onReconnected);
    room.on(RoomEvent.DataReceived, onData);
    return () => {
      room.off(RoomEvent.Connected, onConnected);
      room.off(RoomEvent.Reconnected, onReconnected);
      room.off(RoomEvent.DataReceived, onData);
    };
  }, [room, load, markSeen]);

  useEffect(() => {
    if (!preview) return;
    const t = window.setTimeout(() => setPreview(null), PREVIEW_FOR);
    return () => window.clearTimeout(t);
  }, [preview]);

  // Open, everything in it has been seen.
  useEffect(() => {
    if (open) markSeen(messages);
  }, [open, messages, markSeen]);

  const setOpen = useCallback((next: boolean) => {
    setOpenState(next);
    if (next) setPreview(null);
  }, []);

  const send = useCallback(
    async (content: string) => {
      const m = await transportRef.current.send(content);
      markSeen([m]);
      setMessages((known) => mergeChat(known, [m]));
    },
    [markSeen]
  );

  const identity = me || room.localParticipant.identity;
  const value = useMemo<ChatState>(
    () => ({
      messages,
      loaded,
      me: identity,
      open,
      setOpen,
      unread: unreadCount(messages, seen, identity),
      preview,
      dismissPreview: () => setPreview(null),
      send,
    }),
    [messages, loaded, identity, open, setOpen, seen, preview, send]
  );
  return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>;
}

function useChat(): ChatState | null {
  return useContext(ChatContext);
}

/** How many messages are waiting, for a mark where the chat itself cannot be seen. */
export function useMeetingChatUnread(): number {
  return useChat()?.unread ?? 0;
}

function countLabel(n: number) {
  return n > 9 ? '9+' : String(n);
}

/** The button among the call controls that opens and closes the chat. */
export function MeetingChatButton() {
  const chat = useChat();
  if (!chat) return null;
  const { open, setOpen, unread } = chat;
  const label = open ? 'Close the chat' : unread ? `Open the chat, ${unread} new` : 'Open the chat';
  return (
    <button
      type="button"
      className="lk-button relative"
      aria-label={label}
      aria-pressed={open}
      title={open ? 'Close the chat' : 'Chat with everyone'}
      onClick={() => setOpen(!open)}
      data-testid="meeting-chat-button"
    >
      <MessageSquareText className="h-5 w-5" aria-hidden="true" />
      {unread > 0 && !open && (
        <span
          className="absolute -right-1 -top-1 flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-primary px-1 text-[10px] font-semibold leading-none text-primary-foreground"
          data-testid="meeting-chat-unread"
          aria-hidden="true"
        >
          {countLabel(unread)}
        </span>
      )}
    </button>
  );
}

/** A small mark on a button, for the corner window where the chat does not fit. */
export function UnreadChatMark() {
  const unread = useMeetingChatUnread();
  if (!unread) return null;
  return (
    <span
      className="pointer-events-none absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[9px] font-semibold leading-none text-primary-foreground"
      aria-label={`${unread} new in the chat`}
      data-testid="meeting-chat-unread-compact"
    >
      {countLabel(unread)}
    </span>
  );
}

/**
 * Where the stage and the chat share the room: the chat as a column on the right
 * on a wide screen, and over the video on a narrow one, where a column would
 * leave neither enough room.
 */
export function StageWithChat({ children }: { children: ReactNode }) {
  const chat = useChat();
  return (
    <div className="relative flex h-full min-h-0">
      <div className="relative min-w-0 flex-1">
        {children}
        {chat && !chat.open && chat.preview && <ChatPreview message={chat.preview} onOpen={() => chat.setOpen(true)} onDismiss={chat.dismissPreview} />}
      </div>
      {chat?.open && <ChatPanel chat={chat} />}
    </div>
  );
}

/** A message that came while the chat was closed, over the corner of the video. */
function ChatPreview({ message, onOpen, onDismiss }: { message: MeetingChatMessage; onOpen: () => void; onDismiss: () => void }) {
  return (
    <div
      role="status"
      className="absolute bottom-3 right-3 z-10 flex w-[min(20rem,calc(100%-1.5rem))] items-start gap-2 rounded-lg border border-border bg-background p-3 text-left text-foreground shadow-lg"
      data-testid="meeting-chat-preview"
    >
      <button type="button" className="min-w-0 flex-1 text-left" onClick={onOpen}>
        <span className="block truncate text-sm font-medium">
          {message.senderName}
          {message.guest && <span className="font-normal text-muted-foreground"> (guest)</span>}
        </span>
        <span className="line-clamp-2 break-anywhere text-sm text-muted-foreground">{message.content}</span>
      </button>
      <button
        type="button"
        className="-m-1 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        aria-label="Dismiss"
        onClick={onDismiss}
      >
        <X className="h-4 w-4" aria-hidden="true" />
      </button>
    </div>
  );
}

function ChatPanel({ chat }: { chat: ChatState }) {
  const { messages, loaded, me, setOpen } = chat;
  const list = useRef<HTMLOListElement>(null);
  const panel = useRef<HTMLElement>(null);
  const composer = useRef<HTMLDivElement>(null);
  const stuckToBottom = useRef(true);

  // Toasts keep clear of the chat: beside it when it is a column, above where
  // you type when it covers the video.
  useEffect(() => {
    const el = panel.current;
    if (!el) return;
    const root = document.documentElement;
    const place = () => {
      const beside = getComputedStyle(el).position === 'static';
      root.style.setProperty('--toast-right', beside ? `${el.offsetWidth}px` : '0px');
      root.style.setProperty('--toast-lift', beside ? '0px' : `${composer.current?.offsetHeight ?? 0}px`);
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(el);
    window.addEventListener('resize', place);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', place);
      root.style.removeProperty('--toast-right');
      root.style.removeProperty('--toast-lift');
    };
  }, []);

  // New messages come in at the bottom, and the list follows them unless
  // somebody has scrolled up to read something earlier.
  useLayoutEffect(() => {
    const el = list.current;
    if (el && stuckToBottom.current) el.scrollTop = el.scrollHeight;
  }, [messages.length]);

  return (
    <aside
      ref={panel}
      aria-label="Meeting chat"
      className="absolute inset-0 z-20 flex flex-col bg-background text-foreground lg:static lg:z-auto lg:w-80 lg:shrink-0 lg:border-l lg:border-border"
      data-testid="meeting-chat"
    >
      <div className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-3">
        <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">In-call messages</h2>
        <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Close the chat" onClick={() => setOpen(false)}>
          <X className="h-4 w-4" />
        </Button>
      </div>
      <p className="shrink-0 border-b border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
        Everyone in the call can see these, guests included. They stay with the call afterwards.
      </p>
      <ol
        ref={list}
        className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-3 py-2"
        aria-live="polite"
        onScroll={(e) => {
          const el = e.currentTarget;
          stuckToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {messages.length === 0 && (
          <li className="py-8 text-center text-sm text-muted-foreground">{loaded ? 'No messages yet.' : 'Loading…'}</li>
        )}
        {messages.map((m, i) => {
          const mine = m.senderId === me;
          const head = startsGroup(messages[i - 1], m);
          return (
            <li key={m.id} className={cn('min-w-0', head && i > 0 && 'pt-3')} data-testid="meeting-chat-message">
              {head && (
                <div className="flex items-baseline gap-2">
                  <span className="min-w-0 truncate text-xs font-semibold">
                    {mine ? 'You' : m.senderName}
                    {m.guest && !mine && <span className="font-normal text-muted-foreground"> (guest)</span>}
                  </span>
                  <time dateTime={m.createdAt} title={formatDate(m.createdAt)} className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
                    {formatTime(m.createdAt)}
                  </time>
                </div>
              )}
              <p className="whitespace-pre-wrap break-anywhere text-sm">
                <LinkedText text={m.content} />
              </p>
            </li>
          );
        })}
      </ol>
      <div ref={composer} className="shrink-0">
        <Composer
          onSend={async (text) => {
            stuckToBottom.current = true;
            await chat.send(text);
          }}
        />
      </div>
    </aside>
  );
}

function Composer({ onSend }: { onSend: (text: string) => Promise<void> }) {
  const toast = useToast();
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    box.current?.focus();
  }, []);

  // As tall as what is written, up to a few lines.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  }, [text]);

  const trimmed = text.trim();
  const tooLong = trimmed.length > MAX_CHAT_MESSAGE;
  const submit = async () => {
    if (!trimmed || tooLong || sending) return;
    setSending(true);
    try {
      await onSend(trimmed);
      setText('');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not send the message.'));
    } finally {
      setSending(false);
      box.current?.focus();
    }
  };

  return (
    <form
      className="shrink-0 border-t border-border p-2"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <div className="flex items-end gap-2 rounded-md border border-input bg-background px-2 py-1.5 focus-within:ring-2 focus-within:ring-ring">
        <textarea
          ref={box}
          rows={1}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              void submit();
            }
          }}
          placeholder="Send a message to everyone"
          aria-label="Message to everyone in the call"
          className="max-h-[120px] min-h-[24px] flex-1 resize-none bg-transparent py-0.5 text-sm text-foreground outline-none placeholder:text-muted-foreground"
        />
        <Button
          type="submit"
          size="icon"
          variant="ghost"
          className="h-7 w-7 shrink-0 text-primary"
          disabled={!trimmed || tooLong || sending}
          aria-label="Send"
        >
          <SendHorizontal className="h-4 w-4" />
        </Button>
      </div>
      {tooLong && (
        <p className="mt-1 text-xs text-destructive" role="alert">
          {trimmed.length - MAX_CHAT_MESSAGE} characters over the limit of {MAX_CHAT_MESSAGE}.
        </p>
      )}
    </form>
  );
}

/** The chat of a call that is over, read back where its log is. */
export function ChatTranscript({ messages }: { messages: MeetingChatMessage[] }) {
  return (
    <ol className="max-h-72 space-y-0.5 overflow-y-auto rounded-md border border-border px-3 py-2">
      {messages.map((m, i) => (
        <li key={m.id} className={cn('min-w-0', startsGroup(messages[i - 1], m) && i > 0 && 'pt-2')}>
          {startsGroup(messages[i - 1], m) && (
            <div className="flex items-baseline gap-2">
              <span className="min-w-0 truncate text-xs font-semibold">
                {m.senderName}
                {m.guest && <span className="font-normal text-muted-foreground"> (guest)</span>}
              </span>
              <time dateTime={m.createdAt} title={formatDate(m.createdAt)} className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
                {formatTime(m.createdAt)}
              </time>
            </div>
          )}
          <p className="whitespace-pre-wrap break-anywhere text-sm">
            <LinkedText text={m.content} />
          </p>
        </li>
      ))}
    </ol>
  );
}
