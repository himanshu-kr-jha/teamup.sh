import { useState, useEffect, useLayoutEffect, useRef, useMemo, useCallback } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import {
  Hash, Send, Paperclip, Smile, Settings, ChevronLeft, MessageSquare, User,
  Megaphone, MoreVertical, Pencil, Trash2, Eye, Ban, Check, CheckCheck, PhoneMissed, PhoneOff, PhoneForwarded,
  MessagesSquare, X, Download, Phone, Forward, Mic, Video,
} from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { NotifyMenu } from './NotifyMenu';
import { Input } from '@/shared/components/ui/input';
import { PersonAvatar } from '@/shared/components/UserAvatar';
import { cn, formatRelativeTime } from '@/shared/utils/cn';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/shared/components/ui/dialog';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { callsApi, channelsApi, filesApi } from '@/shared/api/client';
import { useUpload, type UploadedFile } from '@/features/files/useUpload';
import { useToast, useToastStore } from '@/shared/hooks/useToast';
import { apiErrorMessage } from '@/shared/api/errors';
import { AttachmentChip } from '@/features/files/AttachmentChip';
import { StagedFiles, type StagedFile } from '@/features/files/StagedFiles';
import { downloadAttachment } from '@/features/files/download';
import { ForwardContext, ForwardDialog, useForward, type ForwardableMessage } from './ForwardDialog';
import { MentionSuggestions, MessageText, useMentionables } from './MentionUI';
import { insertMention, mentionQuery, suggestPeople, type Mentionable } from './mentions';
import { VideoNoteDialog, VoiceRecorder } from './Recorder';
import { ChannelSettingsDialog } from './ChannelSettingsDialog';
import { NewDirectMessageDialog } from './NewDirectMessageDialog';
import { useAuth } from '@/features/auth/AuthProvider';
import { useUIStore } from '@/shared/store';
import { CreateChannelDialog } from './CreateChannelDialog';
import { usePresence, type PresenceStatus } from '@/features/presence/usePresence';
import { PresenceDot, presenceLabel } from '@/features/presence/PresenceDot';
import { mergeOlder, olderPosition } from './history';
import {
  bumpReplyCount,
  mergeReplies,
  partitionThreads,
  replyCountLabel,
  threadIdOf,
} from './threads';
import {
  QUICK_EMOJI,
  applyReaction,
  hasReacted,
  reactionSummary,
  summariseReactions,
  type ReactionMap,
} from './reactions';
import { callEventView } from './callEvent';
import { applyReadReceipt, type ReadReceipt } from './readReceipts';
import { restoreDraft, restoreStaged, whenVisible } from './composerDraft';
import { focusWhenFree, typesOnOpen } from './composerFocus';
import { applyDelete, applyEdit, isEditFrame } from './liveChanges';

interface Message {
  id: string;
  channelId: string;
  userId: string;
  userName: string;
  userAvatar?: string;
  content: string;
  attachments: any[];
  /** The thread root this is a reply to. Absent on a top-level message. */
  replyTo?: string;
  /** Passed on from another conversation. */
  forwarded?: boolean;
  /** How many replies hang off this message. Only ever set on a root. */
  replyCount?: number;
  /** When this message's thread last moved. */
  lastReplyAt?: string;
  reactions: Record<string, string[]>;
  createdAt: string;
  editedAt?: string;
  deletedAt?: string;
  /** How many other people in the channel have read it. */
  readCount?: number;
  /**
   * "user" for something somebody wrote, "event" for something that happened
   * — a missed call, say. Absent on older messages, which are all "user".
   */
  kind?: string;
  pending?: boolean;
}

/**
 * How long to wait before telling the server how far somebody has read.
 *
 * Long enough that a rapid exchange is one call rather than one per message,
 * short enough that closing the tab straight after reading still records it.
 */
const READ_MARKER_DELAY_MS = 1500;

export function ChatPage() {
  const { user } = useAuth();
  const navigate = useNavigate();
  const { on, state: wsState } = useWebSocket();
  const queryClient = useQueryClient();
  const { activeChannelId, setActiveChannelId } = useUIStore();
  const params = useParams<{ channelId?: string }>();
  const [message, setMessage] = useState('');
  // There is not room for the list and the conversation at once on a phone,
  // so it shows one: the list until you pick something, the conversation after.
  const [showSidebar, setShowSidebar] = useState(() => {
    if (typeof window === 'undefined' || window.innerWidth >= 1024) return true;
    // Arriving at a conversation by its address should show the conversation,
    // not the list you did not ask for.
    return !params.channelId;
  });
  const [createChannelOpen, setCreateChannelOpen] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  // Uploaded but not yet sent. A file used to appear in the channel the instant
  // it finished uploading; now it waits here until you press Send, so it can go
  // on its own or alongside a message.
  const [staged, setStaged] = useState<StagedFile[]>([]);
  const [newChatOpen, setNewChatOpen] = useState(false);
  const [startingCall, setStartingCall] = useState(false);
  // What we last told the server was read, per channel, so scrolling or a
  // re-render does not repeat the call.
  const lastReadRef = useRef<Record<string, string>>({});
  const readTimersRef = useRef<Record<string, number>>({});

  // A pending marker must still be sent if the page is closed first, or the
  // last thing you read is never recorded as read.
  useEffect(() => {
    const timers = readTimersRef.current;
    const pending = lastReadRef.current;
    const flush = () => {
      for (const [id, messageId] of Object.entries(pending)) {
        window.clearTimeout(timers[id]);
        channelsApi.markRead(id, messageId).catch(() => {});
      }
    };
    window.addEventListener('pagehide', flush);
    return () => {
      window.removeEventListener('pagehide', flush);
      flush();
    };
  }, []);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const channelId = params.channelId || activeChannelId;

  // Fetch channels
  const { data: channelsResponse, isLoading: channelsLoading, dataUpdatedAt: channelsUpdatedAt } = useQuery({
    queryKey: ['channels'],
    queryFn: () => channelsApi.list(),
  });

  const channels = channelsResponse?.data || [];
  // The message somebody chose to pass on, if any.
  const [forwarding, setForwarding] = useState<ForwardableMessage | null>(null);
  const openConversationShowsUnread =
    (channels.find((c: any) => c.id === (params.channelId || activeChannelId))?.unreadCount ?? 0) > 0;

  // The newest page. Everything live — an optimistic send, a message off the
  // socket, the catch-up refetch after a reconnect — lands here.
  const { data: messagesResponse, isLoading: messagesLoading } = useQuery({
    queryKey: ['messages', channelId],
    // The newest day with anything in it; earlier days come as you scroll up.
    // A whole day reads as one piece, and is usually far less than a fixed
    // page, so a conversation opens faster.
    queryFn: () => channelsApi.getMessageDay(channelId!),
    enabled: !!channelId,
    staleTime: 0,
    refetchOnMount: 'always',
  });

  // Memoised so the fallback [] doesn't produce a new array every render and
  // retrigger the scroll-to-bottom effect below.
  const recent = useMemo<Message[]>(() => messagesResponse?.data || [], [messagesResponse]);

  /**
   * History paged in above the newest page.
   *
   * Deliberately not in the query cache. The reconnect and tab-visibility
   * catch-up both invalidate ['messages'], which re-reads the newest page —
   * if the pages people had scrolled back through lived there too, every
   * reconnect would throw their place in the conversation away.
   */
  const [older, setOlder] = useState<Message[]>([]);
  const [loadingOlder, setLoadingOlder] = useState(false);
  /** The server had no more to give, so stop asking. */
  const [atStart, setAtStart] = useState(false);
  // The newest day says whether there is anything before it.
  const newestDayIsAll = messagesResponse ? (messagesResponse as { hasMore?: boolean }).hasMore === false : false;
  const reachedTheStart = atStart || (older.length === 0 && newestDayIsAll);
  const scrollRef = useRef<HTMLDivElement>(null);
  // The scroll height just before a prepend, so the position afterwards is
  // arithmetic rather than a guess.
  const heightBeforePrependRef = useRef<number | null>(null);

  /** The thread on screen beside the conversation, if any. */
  const [openThreadId, setOpenThreadId] = useState<string | null>(null);
  // Which replies have already moved a parent's count. A frame can be
  // delivered twice — a reconnect, a duplicate — and one reply must not read
  // as two. Cleared with the channel, because the counts are refetched with it.
  const countedRepliesRef = useRef<Set<string>>(new Set());

  // A different conversation is a different history.
  useEffect(() => {
    setOlder([]);
    setAtStart(false);
    setLoadingOlder(false);
    heightBeforePrependRef.current = null;
    // A thread belongs to the channel it is in; carrying one across to the
    // next conversation would leave it open beside messages it has nothing to
    // do with.
    setOpenThreadId(null);
    countedRepliesRef.current = new Set();
  }, [channelId]);

  const allMessages = useMemo(() => mergeOlder(older, recent), [older, recent]);
  /**
   * The channel timeline, with anything threaded taken out of it.
   *
   * The server already leaves replies out, so this normally removes nothing.
   * It is here for the rows written before threads existed and for the window
   * during a deploy when the two sides disagree — a reply rendered inline is
   * the exact thing threading is here to fix, so the client refuses to render
   * one rather than trusting it will never be sent one.
   */
  const messages = useMemo(() => partitionThreads(allMessages).timeline, [allMessages]);

  const loadOlder = useCallback(async () => {
    if (!channelId || loadingOlder || reachedTheStart) return;
    const cursor = olderPosition(messages);
    // Nothing accepted by the server yet, so there is no point in the history
    // to ask about.
    if (!cursor) return;

    setLoadingOlder(true);
    heightBeforePrependRef.current = scrollRef.current?.scrollHeight ?? null;
    try {
      // The day before, whichever date that is.
      const page = await channelsApi.getMessageDay(channelId, cursor);
      const rows = (page.data ?? []) as Message[];
      // Without this the spinner sits at the top of a conversation asking for ever.
      if (!page.hasMore) setAtStart(true);
      if (rows.length > 0) {
        setOlder((all) => mergeOlder(rows, all));
      } else {
        heightBeforePrependRef.current = null;
      }
    } catch (err) {
      heightBeforePrependRef.current = null;
      toast.error(apiErrorMessage(err, 'Could not load earlier messages.'));
    } finally {
      setLoadingOlder(false);
    }
    // toast is rebuilt every render; the rest is what actually decides this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, loadingOlder, reachedTheStart, messages]);

  /**
   * Keeps the reader where they were when older messages arrive above them.
   *
   * Everything on screen has moved down by exactly the height of what was
   * inserted, so putting that back is the whole job. Before the layout is
   * painted, or it reads as a jump.
   */
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const before = heightBeforePrependRef.current;
    if (!el || before == null) return;
    heightBeforePrependRef.current = null;
    el.scrollTop += el.scrollHeight - before;
  }, [messages]);

  // A quiet day may not fill the window, and a window with no scrollbar gives
  // nobody a way to scroll up for more, so the day before is brought in until
  // it does or there is nothing left.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || messagesLoading || loadingOlder || reachedTheStart || messages.length === 0) return;
    if (el.scrollHeight <= el.clientHeight + 48) void loadOlder();
  }, [messages, messagesLoading, loadingOlder, reachedTheStart, loadOlder]);

  const activeChannel = channels.find((c) => c.id === channelId);
  const { statusOf, personOf } = usePresence();
  // A reaction payload only carries user ids. Availability covers everybody in
  // the organization, including people who are not connected, so it can name
  // whoever reacted — and reactionSummary counts anyone it cannot.
  const nameOf = (id: string) => personOf(id)?.name;
  const toast = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const composerRef = useRef<HTMLInputElement>(null);

  // Opening a conversation, or closing a thread back to it, leaves the cursor
  // in the message box, so nobody has to click it before they can type. Not on
  // a phone, where it would throw the keyboard up over the conversation. A
  // conversation started from a dialog is waited for: the dialog hands focus
  // back to its button as it closes, after this would have run.
  useEffect(() => {
    if (!channelId || openThreadId || !typesOnOpen()) return;
    return focusWhenFree(() => composerRef.current);
  }, [channelId, openThreadId]);

  // Who is being named, while an @-word is typed.
  const mentionables = useMentionables();
  const [mention, setMention] = useState<{ query: string; start: number } | null>(null);
  const [mentionIndex, setMentionIndex] = useState(0);
  const mentionChoices = useMemo(
    () => (mention ? suggestPeople(mention.query, mentionables.filter((p) => p.id !== user?.id)) : []),
    [mention, mentionables, user?.id]
  );
  const followCaret = (text: string, caret: number) => {
    const next = mentionQuery(text, caret);
    setMention(next);
    if (next?.query !== mention?.query) setMentionIndex(0);
  };
  const pickMention = (person: Mentionable) => {
    if (!mention) return;
    const caret = composerRef.current?.selectionStart ?? message.length;
    const next = insertMention(message, mention.start, caret, person);
    setMessage(next.text);
    setMention(null);
    requestAnimationFrame(() => {
      composerRef.current?.focus();
      composerRef.current?.setSelectionRange(next.caret, next.caret);
    });
  };

  // Recording a voice message replaces the message box; a video message has a dialog.
  const [recordingVoice, setRecordingVoice] = useState(false);
  const [recordingVideo, setRecordingVideo] = useState(false);
  /** Uploads one file after another, so several chosen at once each go up with their own progress. */
  const attachFiles = async (files: File[]) => {
    for (const file of files) {
      const uploaded = await upload(file);
      if (uploaded) setStaged((all) => [...all, { ...uploaded, local: file }]);
    }
  };
  const { upload, progress, error: uploadError } = useUpload('chat', channelId || '');

  useEffect(() => {
    if (uploadError) toast.error(uploadError);
    // toast is rebuilt each render; the error is the real trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uploadError]);

  // The messages query goes through getList, so the cache entry is the whole
  // axios response and the array lives on `.data`. Every writer below used to
  // treat the entry itself as the array, which threw inside onMutate and meant
  // the send request was never issued at all.
  const writeMessages = (
    channel: string,
    update: (messages: Message[]) => Message[]
  ) =>
    queryClient.setQueryData(['messages', channel], (old: any) =>
      old ? { ...old, data: update(old.data ?? []) } : old
    );

  /**
   * Changes one message wherever it happens to be held.
   *
   * A message is either in the newest page (the query cache) or in the history
   * paged in above it, and a reaction or an edit can land on either. Writing
   * to only one of the two would leave anything older than fifty messages
   * unreactable — which is the same class of bug as not being able to read it.
   */
  const patchMessage = useCallback(
    (messageId: string, update: (message: Message) => Message) => {
      const applyTo = (list: Message[]) => {
        let touched = false;
        const next = list.map((m) => {
          if (m.id !== messageId) return m;
          touched = true;
          return update(m);
        });
        // Same array back when nothing matched, so React has no re-render to do.
        return touched ? next : list;
      };
      setOlder(applyTo);
      queryClient.setQueryData(['messages', channelId], (old: any) =>
        old ? { ...old, data: applyTo(old.data ?? []) } : old
      );
      // A thread is the third place a message can be. Reacting to a reply, or
      // editing one, has to land where the reply actually is — a reply lives
      // in the same channel as its root, so reactions and read receipts work
      // on it unchanged, and this is what makes that true on screen too.
      if (openThreadId) {
        queryClient.setQueryData(['thread', openThreadId], (old: any) => {
          if (!old?.data) return old;
          const replies = applyTo(old.data.replies ?? []);
          const root = old.data.root?.id === messageId ? update(old.data.root) : old.data.root;
          if (replies === old.data.replies && root === old.data.root) return old;
          return { ...old, data: { ...old.data, root, replies } };
        });
      }
    },
    [channelId, openThreadId, queryClient]
  );

  /**
   * Somebody has read up to a point, so the tick under the messages they just
   * crossed becomes a double one, without anybody reloading.
   *
   * A message lives in up to three places on this page — the pages of history
   * above, the newest page in the query cache, and an open thread — and a
   * receipt has to reach all three for the same reason an edit does: a tick
   * that only updates in one of them is a tick that changes when you scroll.
   */
  const applyReceipt = useCallback(
    (receipt: ReadReceipt) => {
      const me = user?.id;
      if (!me || receipt.channelId !== channelId) return;
      const applyTo = (list: Message[]) => applyReadReceipt(list, receipt, me);

      // A thread's marker crosses only that thread's replies.
      if (receipt.threadId) {
        queryClient.setQueryData(['thread', receipt.threadId], (old: any) => {
          if (!old?.data) return old;
          const replies = applyTo(old.data.replies ?? []);
          return replies === old.data.replies ? old : { ...old, data: { ...old.data, replies } };
        });
        return;
      }

      // The conversation's marker crosses the conversation, a thread's root
      // included, and never a reply.
      setOlder(applyTo);
      queryClient.setQueryData(['messages', channelId], (old: any) => {
        if (!old) return old;
        const data = applyTo(old.data ?? []);
        return data === old.data ? old : { ...old, data };
      });
      if (openThreadId) {
        queryClient.setQueryData(['thread', openThreadId], (old: any) => {
          if (!old?.data?.root) return old;
          const [root] = applyTo([old.data.root]);
          return root === old.data.root ? old : { ...old, data: { ...old.data, root } };
        });
      }
    },
    [channelId, openThreadId, queryClient, user?.id]
  );

  useEffect(() => {
    const unsubscribe = on('chat.read.receipt', (msg) => {
      const receipt = msg.payload as unknown as ReadReceipt;
      if (!receipt?.channelId || !receipt.to) return;
      applyReceipt(receipt);
    });
    return unsubscribe;
  }, [on, applyReceipt]);

  /**
   * Puts a reaction on, or takes it off — the same click either way.
   *
   * Optimistic, because a reaction is a gesture and a gesture that waits for a
   * round trip does not feel like one. Put back if the server disagrees.
   */
  const toggleReaction = useCallback(
    async (messageId: string, emoji: string, current: ReactionMap | undefined) => {
      const userId = user?.id;
      if (!userId) return;
      const action = hasReacted(current, emoji, userId) ? 'remove' : 'add';

      patchMessage(messageId, (m) => ({
        ...m,
        reactions: applyReaction(m.reactions, { emoji, userId, action }),
      }));

      try {
        await (action === 'add'
          ? channelsApi.addReaction(messageId, emoji)
          : channelsApi.removeReaction(messageId, emoji));
      } catch (err) {
        // Undo by applying the opposite, not by restoring a snapshot: somebody
        // else's reaction may have arrived in between and is not ours to drop.
        patchMessage(messageId, (m) => ({
          ...m,
          reactions: applyReaction(m.reactions, {
            emoji,
            userId,
            action: action === 'add' ? 'remove' : 'add',
          }),
        }));
        toast.error(apiErrorMessage(err, 'Could not change that reaction.'));
      }
    },
    // toast is rebuilt every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [user?.id, patchMessage]
  );

  // Somebody else reacting, live. The server excludes whoever reacted from the
  // broadcast, so this only ever carries other people — and applyReaction is
  // idempotent regardless, so a duplicate cannot read as two people.
  useEffect(() => {
    return on('chat.reaction', (msg) => {
      const event = msg.payload as {
        messageId?: string;
        emoji?: string;
        action?: 'add' | 'remove';
        userId?: string;
      };
      if (!event?.messageId || !event.emoji || !event.userId || !event.action) return;
      patchMessage(event.messageId, (m) => ({
        ...m,
        reactions: applyReaction(m.reactions, {
          emoji: event.emoji!,
          userId: event.userId!,
          action: event.action!,
        }),
      }));
    });
  }, [on, patchMessage]);

  /**
   * A reply is sent down the same road as any other message — the same
   * endpoint, the same broadcast — and differs only in where its optimistic
   * copy is put while the server thinks about it: into the thread, never into
   * the timeline.
   */
  const writeThread = (rootId: string, update: (replies: Message[]) => Message[]) =>
    queryClient.setQueryData(['thread', rootId], (old: any) =>
      old?.data ? { ...old, data: { ...old.data, replies: update(old.data.replies ?? []) } } : old
    );

  const sendMessageMutation = useMutation({
    mutationFn: (data: {
      channelId: string;
      content: string;
      tempId: string;
      replyTo?: string;
      attachmentIds?: string[];
    }) => channelsApi.sendMessage(data.channelId, data),
    onMutate: async (variables) => {
      const key = variables.replyTo
        ? ['thread', variables.replyTo]
        : ['messages', variables.channelId];
      await queryClient.cancelQueries({ queryKey: key });
      const previous = queryClient.getQueryData(key);
      const tempMessage: Message = {
        id: variables.tempId,
        channelId: variables.channelId,
        userId: user?.id || '',
        userName: user?.name || 'You',
        content: variables.content,
        attachments: variables.attachmentIds?.length ? staged : [],
        replyTo: variables.replyTo,
        reactions: {},
        createdAt: new Date().toISOString(),
        pending: true,
      };
      // History is oldest-first, so a new message belongs at the end.
      if (variables.replyTo) {
        writeThread(variables.replyTo, (replies) => [...replies, tempMessage]);
      } else {
        writeMessages(variables.channelId, (messages) => [...messages, tempMessage]);
      }
      return { previous, key, attachments: tempMessage.attachments as UploadedFile[] };
    },
    onError: (err, variables, context) => {
      if (context?.key) queryClient.setQueryData(context.key, context.previous);
      toast.error(apiErrorMessage(err, 'Could not send that message.'));
      // The composer was cleared on the way out, so a failure hands it back. A
      // reply keeps its own box in the thread panel, and a draft belongs to the
      // conversation it was written in.
      if (variables.replyTo || variables.channelId !== channelId) return;
      setMessage((current) => restoreDraft(current, variables.content));
      setStaged((current) => restoreStaged(current, context?.attachments ?? []));
    },
    onSuccess: (data, variables) => {
      const sent = { ...(data.data as Message), pending: false };
      if (variables.replyTo) {
        writeThread(variables.replyTo, (replies) =>
          replies.map((m) => (m.id === variables.tempId ? sent : m))
        );
        // Your own reply does not come back over the socket, so the count on
        // the parent is this side's to move — and only once, which is what the
        // ledger of counted replies is for.
        if (!countedRepliesRef.current.has(sent.id)) {
          countedRepliesRef.current.add(sent.id);
          const rootId = variables.replyTo;
          setOlder((all) => bumpReplyCount(all, rootId, sent.createdAt));
          queryClient.setQueryData(['messages', variables.channelId], (old: any) =>
            old ? { ...old, data: bumpReplyCount(old.data ?? [], rootId, sent.createdAt) } : old
          );
        }
        return;
      }
      writeMessages(variables.channelId, (messages) =>
        messages.map((m) => (m.id === variables.tempId ? sent : m))
      );
    },
    // Deliberately no onSettled refetch. onSuccess has already replaced the
    // optimistic copy with the row the server returned, and anything that
    // arrived meanwhile came over the socket — so re-reading the whole history
    // after every message bought nothing and cost a round trip each time.
  });

  // Handle incoming messages via WebSocket
  useEffect(() => {
    const unsubscribe = on('chat.message', (msg) => {
      // The frame carries {message: {...}}; reading the payload directly gave
      // an object with no channelId, so every live message was discarded and
      // the only way to see one was to reload.
      const incoming = ((msg.payload as any)?.message ?? msg.payload) as Message;
      if (!incoming?.id) return;
      // An edit is broadcast on this same type, carrying only the id, the
      // channel and the new text. It is not a new message and must not be
      // appended as one: a message the page is not already holding would
      // otherwise arrive as a ghost with no author and no timestamp. Edits are
      // applied where the message actually is, by the effect below.
      if (isEditFrame(incoming)) return;
      // The sound and the desktop notification are raised by
      // ChatNotifications, which listens wherever you are in the app. This
      // only keeps the conversation on screen up to date.

      // A reply arrives over the same broadcast as anything else — deliberately
      // the same path rather than a second mechanism — and lands in two places
      // instead of the timeline: the parent's reply count, and the thread
      // itself if somebody has it open.
      if (incoming.replyTo) {
        if (!countedRepliesRef.current.has(incoming.id)) {
          countedRepliesRef.current.add(incoming.id);
          const rootId = incoming.replyTo;
          const at = incoming.createdAt;
          setOlder((all) => bumpReplyCount(all, rootId, at));
          queryClient.setQueryData(['messages', incoming.channelId], (old: any) =>
            old ? { ...old, data: bumpReplyCount(old.data ?? [], rootId, at) } : old
          );
        }
        // Anyone with the thread open sees it appear; the cache key is the
        // thread, so this reaches the panel wherever it is mounted.
        queryClient.setQueryData(['thread', incoming.replyTo], (old: any) =>
          old
            ? { ...old, data: { ...old.data, replies: mergeReplies(old.data.replies ?? [], [incoming]) } }
            : old
        );
        return;
      }

      writeMessages(incoming.channelId, (messages) =>
        messages.some((m) => m.id === incoming.id) ? messages : [...messages, incoming]
      );

      // You are reading it, so it is already read. In a hidden tab you are not:
      // the effect on the last message below marks it when you come back.
      if (incoming.channelId === channelId && document.visibilityState === 'visible') {
        markChannelRead(incoming.channelId, incoming.id);
      }
      // Anything for another channel is counted by ChatNotifications, which
      // listens wherever you are — doing it here as well counted it twice.
    });
    return unsubscribe;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, on, queryClient]);

  // Somebody else's edit or delete lands wherever the message is on screen,
  // with the same patch your own does. Its own effect, because patchMessage
  // changes with the open thread and the handler above does not resubscribe.
  useEffect(() => {
    const stopEdits = on('chat.message', (msg) => {
      const frame = (msg.payload as any)?.message as Message | undefined;
      if (!frame?.id || !isEditFrame(frame)) return;
      patchMessage(frame.id, (m) => applyEdit(m, frame));
    });
    const stopDeletes = on('chat.delete', (msg) => {
      const messageId = (msg.payload as { messageId?: string })?.messageId;
      if (!messageId) return;
      patchMessage(messageId, (m) => applyDelete(m, new Date().toISOString()));
    });
    return () => {
      stopEdits();
      stopDeletes();
    };
  }, [on, patchMessage]);

  // Anything sent while the socket was down was never delivered here, so the
  // history is read once when it comes back.
  //
  // Sending used to re-read the whole history every time, which meant a
  // dropped socket was quietly papered over the next time you happened to say
  // something. Catching up when the connection returns is both cheaper and
  // more honest — it does not depend on you being the one to speak next.
  const socketWasDown = useRef(false);
  useEffect(() => {
    if (wsState !== 'ready') {
      socketWasDown.current = true;
      return;
    }
    if (!socketWasDown.current) return;
    socketWasDown.current = false;
    queryClient.invalidateQueries({ queryKey: ['messages'] });
    queryClient.invalidateQueries({ queryKey: ['channels'] });
  }, [wsState, queryClient]);

  // Coming back to the tab after a while, likewise: a socket can be alive and
  // still have missed something while the machine was asleep.
  useEffect(() => {
    const catchUp = () => {
      if (document.visibilityState !== 'visible') return;
      queryClient.invalidateQueries({ queryKey: ['messages'] });
    };
    document.addEventListener('visibilitychange', catchUp);
    return () => document.removeEventListener('visibilitychange', catchUp);
  }, [queryClient]);

  // Handle sent confirmation
  useEffect(() => {
    const unsubscribe = on('chat.sent', (msg) => {
      const { tempId, messageId, timestamp } = msg.payload;
      if (!channelId) return;
      writeMessages(channelId, (messages) =>
        messages.map((m) => (m.id === tempId ? { ...m, id: messageId, createdAt: timestamp, pending: false } : m))
      );
    });
    return unsubscribe;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, on, queryClient]);

  // Scroll to bottom.
  //
  // Keyed on the newest message rather than the list, because the list also
  // changes when history is paged in above — and being yanked back to the
  // bottom every time you scroll up is the thing that makes reading history
  // impossible. Prepending does not move the end of the conversation, so this
  // does not fire for it.
  //
  // Opening a channel jumps; only a message arriving in one you are already
  // reading animates. Smooth-scrolling the initial position meant travelling
  // through the top of the list, which tripped the load-older handler on the
  // way past and fetched a second page nobody had asked for — every time
  // anybody opened any channel.
  const newestMessageId = messages[messages.length - 1]?.id;
  const positionedChannelRef = useRef<string | null>(null);
  useEffect(() => {
    if (!newestMessageId) return;
    const opening = positionedChannelRef.current !== channelId;
    positionedChannelRef.current = channelId ?? null;
    messagesEndRef.current?.scrollIntoView({ behavior: opening ? 'auto' : 'smooth' });
  }, [newestMessageId, channelId]);

  // Reading a conversation marks it read. Nothing used to call this, so
  // last_read_at was never set and every message ever sent stayed unread.
  useEffect(() => {
    if (!channelId || messages.length === 0) return;
    const latest = messages[messages.length - 1];
    if (!latest?.id || latest.pending) return;
    return whenVisible(document, () => markChannelRead(channelId, latest.id));
    // Keyed on the last message so re-reading the same history is one call,
    // and on the open conversation showing as unread: a list read back from
    // the server before it stored the marker brings the old count with it,
    // and the conversation in front of you must not keep a badge for that.
    // A list of conversations that was already on its way when this cleared the
    // count lands with the old one; arriving in the same render as the
    // clearing, it left the flag above unchanged and the badge stuck. So every
    // list that arrives is checked again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, messages[messages.length - 1]?.id, openConversationShowsUnread, channelsUpdatedAt]);

  /**
   * Records how far somebody has read.
   *
   * Best effort: failing to record a read marker must not interrupt reading.
   *
   * Coalesced, because this used to be a request per message per reader, and
   * then a second one — it re-read the whole channel list afterwards purely to
   * set a badge to zero, which is something we already know. In a conversation
   * going back and forth that was the bulk of the traffic, and on a slow link
   * it is traffic competing with the messages themselves.
   */
  const markChannelRead = (id: string, messageId: string) => {
    // The badge is zero the moment you are looking at it, whatever the server
    // has got round to storing.
    // Nothing to clear changes nothing: writing the list anyway would count as
    // a fresh one, and set off the check that called this again.
    queryClient.setQueryData(['channels'], (old: any) => {
      if (!old?.data?.some((c: any) => c.id === id && c.unreadCount)) return undefined;
      return {
        ...old,
        data: old.data.map((c: any) => (c.id === id && c.unreadCount ? { ...c, unreadCount: 0 } : c)),
      };
    });

    if (lastReadRef.current[id] === messageId) return;
    lastReadRef.current[id] = messageId;

    // Only the furthest point matters, so a burst becomes one call rather than
    // one per message.
    window.clearTimeout(readTimersRef.current[id]);
    readTimersRef.current[id] = window.setTimeout(() => {
      const furthest = lastReadRef.current[id];
      if (!furthest) return;
      channelsApi.markRead(id, furthest).catch(() => {
        // It will be marked again on the next message.
        delete lastReadRef.current[id];
      });
    }, READ_MARKER_DELAY_MS);
  };

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    if (!channelId) return;
    // A file on its own is a message, so an empty box is fine if something is
    // attached.
    if (!message.trim() && staged.length === 0) return;
    const tempId = `temp-${Date.now()}`;
    sendMessageMutation.mutate({
      channelId,
      content: message,
      tempId,
      attachmentIds: staged.map((f) => f.id),
    });
    setMessage('');
    setStaged([]);
  };

  /**
   * Says something in a thread.
   *
   * The same endpoint as any other message with `replyTo` set, so a reply is
   * stored, broadcast, searched and counted like everything else — the only
   * thing threading changes is where it is read.
   */
  const sendReply = (rootId: string, content: string) => {
    if (!channelId || !content.trim()) return;
    sendMessageMutation.mutate({
      channelId,
      content,
      tempId: `temp-${Date.now()}`,
      replyTo: rootId,
    });
  };

  // Calls the whole channel: everybody in it is rung. A public channel has the
  // whole organization in it, a private one only its members, so the channel
  // decides who can be reached without a separate rule here.
  const startCall = async () => {
    if (!channelId || startingCall) return;
    setStartingCall(true);
    try {
      const { data: call } = await callsApi.create({
        channelId,
        // A call: microphone on, camera off until somebody chooses video.
        callType: 'audio',
        title: activeChannel?.name ?? undefined,
      });
      navigate(`/calls/${call.id}`);
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not start that call.'));
    } finally {
      setStartingCall(false);
    }
  };

  const channelSubtitle = !channelId
    ? 'Pick a conversation to start reading'
    : activeChannel?.type === 'dm'
      ? presenceLabel(
          statusOf(activeChannel?.counterpartId),
          personOf(activeChannel?.counterpartId)?.note
        )
      : activeChannel?.type === 'announcement'
        ? 'Announcements — only administrators can post'
        : `${activeChannel?.memberCount ?? 0} ${activeChannel?.memberCount === 1 ? 'member' : 'members'}`;

  const rooms = channels.filter((c: any) => c.type !== 'dm');
  const directMessages = channels.filter((c: any) => c.type === 'dm');

  /**
   * Esc closes the open thread, or else the conversation, back to the list.
   *
   * Only when nothing else is using Esc first: a dialog, a menu, a viewer, the
   * mention suggestions or a message being edited all close themselves, and
   * those that do mark the key as handled.
   */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      if (document.querySelector('[role="dialog"], [role="menu"], [role="listbox"]')) return;
      const target = e.target as HTMLElement | null;
      if (target?.getAttribute('aria-label') === 'Edit message') return;
      if (openThreadId) {
        setOpenThreadId(null);
        return;
      }
      if (!channelId) return;
      composerRef.current?.blur();
      setActiveChannelId(null);
      setShowSidebar(true);
      navigate('/chat');
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [openThreadId, channelId, navigate, setActiveChannelId]);

  const handleChannelClick = (id: string) => {
    setActiveChannelId(id);
    navigate(`/chat/${id}`);
    // On a phone, picking a conversation is how you get to it.
    if (window.innerWidth < 1024) setShowSidebar(false);
  };

  if (channelsLoading) {
    return <div className="flex h-full items-center justify-center">Loading channels...</div>;
  }

  return (
    <ForwardContext.Provider value={setForwarding}>
    <div className="flex h-full flex-col bg-background">

      {/* Positioned, so the thread panel's full-screen form on a narrow
          window covers the conversation rather than the whole application. */}
      <div className="relative flex h-full min-w-0">
        {/* Sidebar */}
        <aside className={cn(
          'z-30 w-full shrink-0 border-r border-border bg-card lg:relative lg:w-72',
          // On a phone this is the whole screen, and only one of the two is
          // shown at a time.
          showSidebar ? 'block' : 'hidden lg:block'
        )}>
          <div className="flex h-full flex-col">
            {/* Header */}
            <div className="flex h-16 items-center justify-between border-b border-border px-4">
              <Link to="/chat" className="text-lg font-semibold">
                Conversations
              </Link>
              <span className="text-xs text-muted-foreground">
                {channels.length} {channels.length === 1 ? 'open' : 'open'}
              </span>
            </div>

            {/* Channels and conversations, kept apart: a room and a person
                are not the same kind of thing. */}
            <div className="flex-1 overflow-y-auto p-2">
              {channels.length === 0 && (
                <p className="px-3 py-8 text-center text-sm text-muted-foreground">
                  Nothing here yet. Start a chat or create a channel.
                </p>
              )}

              {[
                { label: 'Channels', items: rooms },
                { label: 'Direct messages', items: directMessages },
              ].map((group) =>
                group.items.length === 0 ? null : (
                  <div key={group.label} className="mb-4">
                    <p className="px-3 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      {group.label}
                    </p>
                    <div className="space-y-0.5">
                      {group.items.map((channel) => {
                        const active = channel.id === channelId;
                        return (
                          <button
                            key={channel.id}
                            onClick={() => handleChannelClick(channel.id)}
                            className={cn(
                              'flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm transition-colors',
                              active
                                ? 'bg-primary text-primary-foreground font-medium'
                                : 'text-foreground/80 hover:bg-accent hover:text-accent-foreground'
                            )}
                          >
                            {channel.type === 'dm' ? (
                              // A conversation with a person shows whether that
                              // person is around, which is the thing you want to
                              // know before starting to type.
                              <span className="relative -my-1 flex shrink-0">
                                <PersonAvatar userId={channel.counterpartId} name={channel.name} className="h-6 w-6" fallbackClassName="text-[10px]" />
                                <PresenceDot
                                  status={statusOf(channel.counterpartId)}
                                  note={personOf(channel.counterpartId)?.note}
                                  className="absolute -bottom-0.5 -right-0.5 h-2 w-2"
                                />
                              </span>
                            ) : channel.type === 'announcement' ? (
                              <Megaphone className="h-4 w-4 shrink-0 opacity-70" />
                            ) : (
                              <Hash className="h-4 w-4 shrink-0 opacity-70" />
                            )}
                            <span className={cn('flex-1 truncate', channel.unreadCount > 0 && !active && 'font-semibold')}>
                              {channel.name || 'Unnamed'}
                            </span>
                            {channel.unreadCount > 0 && (
                              <span
                                className={cn(
                                  'flex h-5 min-w-[1.25rem] items-center justify-center rounded-full px-1.5 text-xs font-semibold',
                                  active ? 'bg-primary-foreground/20 text-primary-foreground' : 'bg-primary text-primary-foreground'
                                )}
                              >
                                {channel.unreadCount > 9 ? '9+' : channel.unreadCount}
                              </span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )
              )}
            </div>

            {/* New channel button */}
            <div className="space-y-2 p-2 border-t border-border">
              <Button variant="outline" className="w-full" onClick={() => setNewChatOpen(true)}>
                <User className="mr-2 h-4 w-4" />
                New chat
              </Button>
              <Button variant="outline" className="w-full" onClick={() => setCreateChannelOpen(true)}>
                <MessageSquare className="mr-2 h-4 w-4" />
                New Channel
              </Button>
            </div>
          </div>
        </aside>

        {/* Main chat area */}
        <div className={cn(
          'flex min-w-0 flex-1 flex-col',
          showSidebar ? 'hidden lg:flex' : 'flex'
        )}>
          {!channelId ? (
            // Nothing open: say so, rather than a conversation's header and a
            // message box with nowhere to send to.
            <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground" data-testid="no-conversation">
              <MessageSquare className="h-12 w-12 opacity-40" aria-hidden="true" />
              <p className="text-lg font-medium text-foreground">Pick a conversation</p>
              <p className="text-sm">Choose one on the left, or start a new chat.</p>
            </div>
          ) : (
          <>
          {/* Channel header */}
          <div className="flex h-16 items-center gap-4 px-4 border-b border-border">
            <div className="flex min-w-0 items-center gap-2">
              <Button
                variant="ghost"
                size="icon"
                className="shrink-0 lg:hidden"
                aria-label="Back to conversations"
                onClick={() => setShowSidebar(true)}
              >
                <ChevronLeft className="h-5 w-5" />
              </Button>
              {activeChannel?.type === 'dm' ? (
                <PersonAvatar userId={activeChannel?.counterpartId} name={activeChannel?.name} className="h-9 w-9" />
              ) : (
                <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/10">
                  {activeChannel?.type === 'announcement' ? (
                    <Megaphone className="h-5 w-5 text-primary" />
                  ) : (
                    <Hash className="h-5 w-5 text-primary" />
                  )}
                </div>
              )}
              <div className="min-w-0">
                <h2 className="flex items-center gap-2 truncate font-semibold leading-tight">
                  {activeChannel?.name || (channelId ? 'Channel' : 'No channel selected')}
                  {activeChannel?.type === 'dm' && (
                    <PresenceDot
                      status={statusOf(activeChannel?.counterpartId)}
                      note={personOf(activeChannel?.counterpartId)?.note}
                    />
                  )}
                </h2>
                <p className="truncate text-sm text-muted-foreground">{channelSubtitle}</p>
              </div>
            </div>
            <div className="flex-1" />
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="icon"
                aria-label="Call"
                title="Call. Your microphone is on; turn your camera on in the call if you want video."
                disabled={!activeChannel || startingCall}
                onClick={startCall}
              >
                <Phone className="h-5 w-5" />
              </Button>
              <NotifyMenu channelId={activeChannel?.id} level={activeChannel?.notifyLevel} />
              <Button
                variant="ghost"
                size="icon"
                aria-label="Channel settings"
                disabled={!activeChannel}
                onClick={() => setShowSettings(true)}
              >
                <Settings className="h-5 w-5" />
              </Button>
            </div>
          </div>

          {/* Messages */}
          <div
            ref={scrollRef}
            className="flex-1 space-y-1 overflow-y-auto p-4"
            onScroll={(e) => {
              // Near the top means somebody is reading backwards. Fetching
              // before they hit the very top means the next page is usually
              // already there when they get to it.
              if (e.currentTarget.scrollTop < 200) void loadOlder();
            }}
          >
            {messagesLoading ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                Loading messages…
              </div>
            ) : messages.length === 0 ? (
              <div className="flex h-full flex-col items-center justify-center text-muted-foreground">
                <MessageSquare className="mb-4 h-12 w-12 opacity-40" aria-hidden="true" />
                <p className="text-lg">No messages yet</p>
                <p className="text-sm">
                  {activeChannel?.type === 'dm'
                    ? `Say hello to ${activeChannel?.name ?? 'them'}.`
                    : 'Be the first to say something.'}
                </p>
              </div>
            ) : (
              <>
                {/* One fixed height for all three states. Swapping a 52px
                    button for a 32px line of text would shift everything below
                    it by twenty pixels at exactly the moment somebody is
                    reading — which is the same jump the scroll restoring below
                    exists to prevent.

                    Scrolling is also not the only way up: a keyboard has no
                    scroll gesture, and a channel shorter than the window has
                    no scrollbar to use, so there is a real control here. */}
                <div className="flex h-12 shrink-0 items-center justify-center">
                  {loadingOlder ? (
                    <p className="text-xs text-muted-foreground">Loading earlier messages…</p>
                  ) : reachedTheStart ? (
                    <p className="px-4 text-center text-xs text-muted-foreground">
                      This is the beginning of{' '}
                      {activeChannel?.type === 'dm'
                        ? `your conversation with ${activeChannel?.name ?? 'them'}`
                        : activeChannel?.name
                          ? `#${activeChannel.name}`
                          : 'this conversation'}
                      .
                    </p>
                  ) : (
                    <Button variant="ghost" size="sm" className="text-xs" onClick={() => void loadOlder()}>
                      Load earlier messages
                    </Button>
                  )}
                </div>

                {messages.map((msg, i) => {
                  const previous = messages[i - 1];
                  // A run of messages from one person in quick succession
                  // reads as one turn in the conversation, so only the first
                  // carries their name and picture.
                  const sameAuthor = previous?.userId === msg.userId;
                  const closeInTime =
                    previous != null &&
                    new Date(msg.createdAt).getTime() - new Date(previous.createdAt).getTime() < 5 * 60 * 1000;
                  const newDay = !previous || !sameDay(previous.createdAt, msg.createdAt);

                  if (msg.kind === 'event') {
                    return (
                      <div key={msg.id}>
                        {newDay && <DaySeparator when={msg.createdAt} />}
                        <CallEvent message={msg} isOwn={msg.userId === user?.id} />
                      </div>
                    );
                  }

                  return (
                    <div key={msg.id}>
                      {newDay && <DaySeparator when={msg.createdAt} />}
                      <MessageBubble
                        message={msg}
                        isOwn={msg.userId === user?.id}
                        status={statusOf(msg.userId)}
                        note={personOf(msg.userId)?.note}
                        grouped={sameAuthor && closeInTime && !newDay}
                        currentUserId={user?.id}
                        nameOf={nameOf}
                        onOpenThread={() => setOpenThreadId(threadIdOf(msg))}
                        onToggleReaction={toggleReaction}
                        onPatch={patchMessage}
                      />
                    </div>
                  );
                })}
                <div ref={messagesEndRef} />
              </>
            )}
          </div>

          {/* Message input */}
          {staged.length > 0 && (
            <StagedFiles
              className="border-t border-border bg-muted/30 px-4 py-2"
              files={staged}
              onRemove={(file) => {
                setStaged((all) => all.filter((f) => f.id !== file.id));
                // Throw the bytes away too, rather than leaving them in
                // storage for the sweep to find a day later.
                filesApi.discard(file.id).catch(() => {
                  // Best effort: the sweep collects it either way.
                });
              }}
            />
          )}

          <form onSubmit={handleSend} className="p-4 border-t border-border">
            <div className="flex items-end gap-2">
              <input
                ref={fileInputRef}
                type="file"
                multiple
                className="hidden"
                aria-label="Attach a file"
                onChange={async (e) => {
                  const files = Array.from(e.target.files ?? []);
                  e.target.value = '';
                  if (files.length === 0 || !channelId) return;
                  await attachFiles(files);
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="shrink-0"
                disabled={!channelId || progress !== null}
                onClick={() => fileInputRef.current?.click()}
                aria-label="Attach a file"
              >
                {progress !== null ? (
                  <span className="text-xs font-medium">{progress}%</span>
                ) : (
                  <Paperclip className="h-5 w-5" />
                )}
              </Button>
              {recordingVoice ? (
                <VoiceRecorder
                  onDone={(file) => {
                    setRecordingVoice(false);
                    void attachFiles([file]);
                  }}
                  onCancel={(problem) => {
                    setRecordingVoice(false);
                    if (problem) toast.error(problem);
                  }}
                />
              ) : (
              <div className="flex-1 relative">
                {mentionChoices.length > 0 && (
                  <MentionSuggestions
                    people={mentionChoices}
                    active={Math.min(mentionIndex, mentionChoices.length - 1)}
                    onPick={pickMention}
                    onHover={setMentionIndex}
                  />
                )}
                <Input
                  ref={composerRef}
                  value={message}
                  onChange={(e) => {
                    setMessage(e.target.value);
                    followCaret(e.target.value, e.target.selectionStart ?? e.target.value.length);
                  }}
                  onSelect={(e) => followCaret(e.currentTarget.value, e.currentTarget.selectionStart ?? 0)}
                  onBlur={() => setMention(null)}
                  aria-autocomplete="list"
                  aria-expanded={mentionChoices.length > 0}
                  placeholder={
                    activeChannel?.type === 'dm'
                      ? `Message ${activeChannel?.name ?? ''}`.trim()
                      : activeChannel?.name
                        ? `Message #${activeChannel.name}`
                        : 'Type a message…'
                  }
                  aria-label="Message"
                  disabled={!channelId}
                  className="pr-10"
                  onKeyDown={(e) => {
                    if (mentionChoices.length > 0) {
                      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                        e.preventDefault();
                        const step = e.key === 'ArrowDown' ? 1 : -1;
                        setMentionIndex((i) => (i + step + mentionChoices.length) % mentionChoices.length);
                        return;
                      }
                      if (e.key === 'Enter' || e.key === 'Tab') {
                        e.preventDefault();
                        pickMention(mentionChoices[Math.min(mentionIndex, mentionChoices.length - 1)]);
                        return;
                      }
                      if (e.key === 'Escape') {
                        // Closes the suggestions, not the conversation.
                        e.preventDefault();
                        setMention(null);
                        return;
                      }
                    }
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault();
                      handleSend(e);
                    }
                  }}
                />
                <div className="absolute bottom-1 right-1 flex items-center gap-1">
                  {/* This did nothing at all. It puts an emoji in what you are
                      writing — reacting to somebody else's message is the
                      picker on the message itself. */}
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon"
                        type="button"
                        aria-label="Emoji"
                        disabled={!channelId}
                        className="h-8 w-8"
                      >
                        <Smile className="h-4 w-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="flex min-w-0 gap-0.5 p-1">
                      {QUICK_EMOJI.map((emoji) => (
                        <button
                          key={emoji}
                          type="button"
                          aria-label={`Insert ${emoji}`}
                          onClick={() => setMessage((text) => text + emoji)}
                          className="rounded p-1.5 text-lg leading-none transition-colors hover:bg-accent"
                        >
                          {emoji}
                        </button>
                      ))}
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </div>
              )}
              {!recordingVoice && (
                <>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="shrink-0"
                    disabled={!channelId || progress !== null}
                    onClick={() => setRecordingVoice(true)}
                    aria-label="Record a voice message"
                    title="Record a voice message"
                  >
                    <Mic className="h-5 w-5" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="shrink-0"
                    disabled={!channelId || progress !== null}
                    onClick={() => setRecordingVideo(true)}
                    aria-label="Record a video message"
                    title="Record a video message"
                  >
                    <Video className="h-5 w-5" />
                  </Button>
                </>
              )}
              <Button
                type="submit"
                size="icon"
                disabled={!message.trim() && staged.length === 0}
                className="shrink-0"
                aria-label="Send"
              >
                <Send className="h-5 w-5" />
              </Button>
            </div>
          </form>
          </>
          )}
        </div>

        {/* The thread, beside the conversation it came out of — so the channel
            is still readable while a side conversation happens, which is the
            whole reason for having threads at all. On a narrow screen there is
            no beside, so it takes the space. */}
        {openThreadId && (
          <ThreadPanel
            rootId={openThreadId}
            currentUserId={user?.id}
            statusOf={statusOf}
            personOf={personOf}
            nameOf={nameOf}
            onToggleReaction={toggleReaction}
            onPatch={patchMessage}
            onSend={(content) => sendReply(openThreadId, content)}
            onClose={() => setOpenThreadId(null)}
          />
        )}
      </div>

      <CreateChannelDialog
        open={createChannelOpen}
        onOpenChange={setCreateChannelOpen}
        onCreated={(id) => {
          setActiveChannelId(id);
          navigate(`/chat/${id}`);
        }}
      />

      <NewDirectMessageDialog
        open={newChatOpen}
        onOpenChange={setNewChatOpen}
        onOpened={(id) => {
          queryClient.invalidateQueries({ queryKey: ['channels'] });
          setActiveChannelId(id);
          navigate(`/chat/${id}`);
        }}
      />

      <ChannelSettingsDialog
        channel={showSettings ? activeChannel ?? null : null}
        onOpenChange={(open) => !open && setShowSettings(false)}
      />
    </div>
      <ForwardDialog message={forwarding} onClose={() => setForwarding(null)} />
      <VideoNoteDialog
        open={recordingVideo}
        onClose={() => setRecordingVideo(false)}
        onDone={(file) => void attachFiles([file])}
      />
    </ForwardContext.Provider>
  );
}

function sameDay(a: string, b: string) {
  const x = new Date(a);
  const y = new Date(b);
  return x.toDateString() === y.toDateString();
}

/** "Today", "Yesterday", or the date — so a long history is navigable. */
function DaySeparator({ when }: { when: string }) {
  const day = new Date(when);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);

  const label =
    day.toDateString() === today.toDateString()
      ? 'Today'
      : day.toDateString() === yesterday.toDateString()
        ? 'Yesterday'
        : day.toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' });

  return (
    <div className="my-4 flex items-center gap-3">
      <div className="h-px flex-1 bg-border" />
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <div className="h-px flex-1 bg-border" />
    </div>
  );
}

/**
 * Something that happened rather than something anybody said.
 *
 * Centred and unattributed, because a missed call is not a message from the
 * person who called — it is the conversation telling you what you missed, and
 * dressing it as their message would have you replying to nobody.
 */
function CallEvent({ message, isOwn }: { message: Message; isOwn: boolean }) {
  // The caller knows they called; what they did not know is how it went. The
  // other person needs to be told it happened at all, and how.
  const view = callEventView(message.content, isOwn, message.userName ?? '');
  const Icon = view.kind === 'declined' ? PhoneOff : view.kind === 'busy' && isOwn ? PhoneForwarded : PhoneMissed;
  return (
    <div className="flex justify-center py-1.5" data-testid="call-event" data-kind={view.kind}>
      <span
        className={cn(
          'flex items-center gap-2 rounded-full px-3 py-1 text-xs',
          view.alert ? 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300' : 'bg-muted text-muted-foreground'
        )}
      >
        <Icon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
        <span>{view.text}</span>
        <span className={view.alert ? 'opacity-80' : undefined}>
          {new Date(message.createdAt).toLocaleTimeString(undefined, {
            hour: '2-digit',
            minute: '2-digit',
          })}
        </span>
      </span>
    </div>
  );
}

function MessageBubble({
  message,
  isOwn,
  status,
  note,
  grouped,
  currentUserId,
  nameOf,
  onOpenThread,
  onToggleReaction,
  onPatch,
}: {
  message: Message;
  isOwn: boolean;
  status: PresenceStatus;
  note?: string;
  /** Part of a run from the same person: no repeated name or picture. */
  grouped: boolean;
  currentUserId?: string;
  nameOf: (userId: string) => string | undefined;
  /**
   * Opens the thread on this message. Absent inside a thread, where there is
   * nowhere further to go: a reply to a reply joins this same conversation, so
   * offering to open another one from in here would be a lie.
   */
  onOpenThread?: () => void;
  onToggleReaction: (messageId: string, emoji: string, current: ReactionMap | undefined) => void;
  onPatch: (messageId: string, update: (message: Message) => Message) => void;
}) {
  const toast = useToast();
  const forward = useForward();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(message.content);
  const [showingReads, setShowingReads] = useState(false);
  const pills = summariseReactions(message.reactions, currentUserId);

  const deleted = !!message.deletedAt;
  const time = new Date(message.createdAt).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  });
  // Editing closes five minutes after sending, so a conversation people have
  // already read does not change underneath them.
  const withinEditWindow =
    Date.now() - new Date(message.createdAt).getTime() < 5 * 60 * 1000;

  // Applied where the message actually is rather than by re-reading the
  // channel: invalidating only re-fetches the newest page, so deleting
  // something from further back used to leave it on screen once history could
  // be scrolled at all.
  const saveEdit = async () => {
    const content = draft.trim();
    if (!content || content === message.content) {
      setEditing(false);
      return;
    }
    try {
      await channelsApi.editMessage(message.id, content);
      setEditing(false);
      onPatch(message.id, (m) => applyEdit(m, { content, editedAt: new Date().toISOString() }));
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not edit that message.'));
    }
  };

  const remove = async () => {
    try {
      await channelsApi.deleteMessage(message.id);
      onPatch(message.id, (m) => applyDelete(m, new Date().toISOString()));
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not delete that message.'));
    }
  };

  return (
    <>
      <div
        className={cn(
          'group flex w-full items-end gap-2',
          isOwn ? 'justify-end' : 'justify-start',
          grouped ? 'mt-0.5' : 'mt-3'
        )}
      >
        {!isOwn &&
          (grouped ? (
            <div className="w-8 shrink-0" aria-hidden="true" />
          ) : (
            <div className="relative shrink-0">
              <PersonAvatar userId={message.userId} name={message.userName} src={message.userAvatar} />
              <PresenceDot status={status} note={note} className="absolute -bottom-0.5 -right-0.5" />
            </div>
          ))}

        <div className={cn('flex min-w-0 max-w-[75%] flex-col', isOwn ? 'items-end' : 'items-start')}>
          {!isOwn && !grouped && (
            <p className="mb-1 px-1 text-xs font-medium text-muted-foreground">{message.userName}</p>
          )}

          {/* The positioning context for the timestamp below, so it stays
              level with the bubble rather than being pushed down the side of
              whatever reactions are underneath it. */}
          <div className="relative flex min-w-0 max-w-full items-end gap-1">
            {/* The menu sits on the side the message came from, so it never
                covers the text. */}
            {isOwn && !deleted && (
              <MessageMenu
                canEdit={withinEditWindow}
                canDelete
                onEdit={() => {
                  setDraft(message.content);
                  setEditing(true);
                }}
                onDelete={remove}
                onReply={onOpenThread}
                onShowReads={() => setShowingReads(true)}
                attachments={message.attachments}
                onForward={forward && !message.pending ? () => forward(message) : undefined}
              />
            )}

            <div
              className={cn(
                'w-fit max-w-full break-anywhere rounded-2xl px-3.5 py-2 text-sm shadow-sm',
                deleted
                  ? 'border border-dashed border-border bg-transparent italic text-muted-foreground'
                  : isOwn
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-foreground',
                !grouped && !deleted && (isOwn ? 'rounded-tr-sm' : 'rounded-tl-sm'),
                message.pending && 'opacity-60'
              )}
            >
              {deleted ? (
                <span className="flex items-center gap-1.5">
                  <Ban className="h-3.5 w-3.5" aria-hidden="true" />
                  This message was deleted
                </span>
              ) : editing ? (
                <div className="space-y-2">
                  <Input
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    aria-label="Edit message"
                    className="h-8 bg-background text-foreground"
                    autoFocus
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault();
                        void saveEdit();
                      }
                      if (e.key === 'Escape') setEditing(false);
                    }}
                  />
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" className="h-6 text-xs" onClick={() => setEditing(false)}>
                      Cancel
                    </Button>
                    <Button size="sm" className="h-6 text-xs" onClick={saveEdit}>
                      Save
                    </Button>
                  </div>
                </div>
              ) : (
                <>
                  {message.forwarded && (
                    <p
                      className={cn(
                        'mb-1 flex items-center gap-1 text-xs italic',
                        isOwn ? 'text-primary-foreground/75' : 'text-muted-foreground'
                      )}
                      data-testid="forwarded-label"
                    >
                      <Forward className="h-3 w-3" aria-hidden="true" />
                      Forwarded
                    </p>
                  )}
                  {message.content && <MessageText content={message.content} isOwn={isOwn} />}
                  {message.attachments?.length > 0 && (
                    <div className={cn('flex flex-wrap gap-2', message.content && 'mt-2')}>
                      {message.attachments.map((a: any) => (
                        <AttachmentChip key={a.id} attachment={a} />
                      ))}
                    </div>
                  )}
                </>
              )}
            </div>

            {!deleted && (
              <ReactionPicker
                onPick={(emoji) => onToggleReaction(message.id, emoji, message.reactions)}
              />
            )}

            {!isOwn && !deleted && (
              <MessageMenu
                canEdit={false}
                canDelete={false}
                onReply={onOpenThread}
                onShowReads={() => setShowingReads(true)}
                attachments={message.attachments}
                onForward={forward && !message.pending ? () => forward(message) : undefined}
              />
            )}

            {/* Out of the flow: shown on hover without moving anything. */}
            <span
              className={cn(
                'pointer-events-none absolute bottom-0 flex items-center gap-1 whitespace-nowrap text-[0.7rem] text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100',
                isOwn ? 'right-full mr-2' : 'left-full ml-2',
                (message.pending || (isOwn && !deleted)) && 'opacity-100'
              )}
            >
              {message.pending ? (
                'Sending…'
              ) : (
                <>
                  {time}
                  {message.editedAt && !deleted && <span>· edited</span>}
                  {/* Only the sender is told whether it landed; showing it to
                      everybody would just be noise. */}
                  {isOwn && !deleted && (
                    <span title={message.readCount ? `Read by ${message.readCount}` : 'Not read yet'}>
                      {message.readCount ? (
                        <CheckCheck className="h-3.5 w-3.5 text-primary" aria-label="Read" />
                      ) : (
                        <Check className="h-3.5 w-3.5" aria-label="Sent" />
                      )}
                    </span>
                  )}
                </>
              )}
            </span>
          </div>

          {/* The way into the thread. Under the message and always visible,
              not on hover and not in a menu: it is the only sign that a
              conversation is happening down here, and a reply nobody can see
              a way to reach is the defect this replaced. It survives the
              parent being deleted — the tombstone keeps its place precisely so
              its replies still have somewhere to hang. */}
          {(message.replyCount ?? 0) > 0 && onOpenThread && (
            <button
              type="button"
              onClick={onOpenThread}
              className={cn(
                'mt-1 flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium text-primary transition-colors hover:bg-primary/10',
                isOwn ? 'self-end' : 'self-start'
              )}
            >
              <MessagesSquare className="h-3.5 w-3.5" aria-hidden="true" />
              {replyCountLabel(message.replyCount ?? 0)}
              {message.lastReplyAt && (
                <span className="font-normal text-muted-foreground">
                  · {formatRelativeTime(message.lastReplyAt)}
                </span>
              )}
            </button>
          )}

          {/* Under the message, where a reaction belongs: it is about the
              message, not another one. */}
          {pills.length > 0 && !deleted && (
            <div className={cn('mt-1 flex flex-wrap gap-1', isOwn ? 'justify-end' : 'justify-start')}>
              {pills.map((pill) => {
                const who = reactionSummary(pill.userIds, currentUserId, nameOf);
                return (
                  <button
                    key={pill.emoji}
                    type="button"
                    // Clicking a reaction you are already on takes it back;
                    // that is the whole interaction, in one control.
                    onClick={() => onToggleReaction(message.id, pill.emoji, message.reactions)}
                    title={`${who} reacted with ${pill.emoji}`}
                    aria-label={`${pill.emoji}, ${who}`}
                    aria-pressed={pill.mine}
                    className={cn(
                      'flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs leading-none transition-colors',
                      pill.mine
                        ? 'border-primary/50 bg-primary/10 text-primary'
                        : 'border-border bg-muted/60 text-muted-foreground hover:bg-muted'
                    )}
                  >
                    <span aria-hidden="true">{pill.emoji}</span>
                    <span className="font-medium tabular-nums">{pill.count}</span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>

      <ReadByDialog
        messageId={showingReads ? message.id : null}
        onClose={() => setShowingReads(false)}
      />
    </>
  );
}

/**
 * The emoji offered on a message.
 *
 * Its own control rather than an item in the actions menu: reacting is meant
 * to be one gesture, and burying it two clicks deep is how a feature ends up
 * unused.
 */
function ReactionPicker({ onPick }: { onPick: (emoji: string) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Add a reaction"
          className="h-6 w-6 shrink-0 opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100"
        >
          <Smile className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="center" className="flex min-w-0 gap-0.5 p-1">
        {QUICK_EMOJI.map((emoji) => (
          <button
            key={emoji}
            type="button"
            aria-label={`React with ${emoji}`}
            onClick={() => onPick(emoji)}
            className="rounded p-1.5 text-lg leading-none transition-colors hover:bg-accent"
          >
            {emoji}
          </button>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** Saves one of a message's files, saying so if it cannot. */
async function saveAttachment(a: { objectKey: string; filename: string }) {
  try {
    await downloadAttachment(a.objectKey, a.filename);
  } catch (err) {
    useToastStore.getState().push({
      title: 'Something went wrong',
      description: apiErrorMessage(err, 'Could not download that file.'),
      variant: 'destructive',
    });
  }
}

/** The actions available on a single message. */
function MessageMenu({
  canEdit,
  canDelete,
  onEdit,
  onDelete,
  onReply,
  onShowReads,
  attachments,
  onForward,
}: {
  /** Passes it on to other conversations. */
  onForward?: () => void;
  /** Files on the message, each of which can be saved from here. */
  attachments?: Array<{ id: string; objectKey: string; filename: string }>;
  canEdit: boolean;
  canDelete: boolean;
  onEdit?: () => void;
  onDelete?: () => void;
  /** Absent inside a thread, where the reply box below is already the answer. */
  onReply?: () => void;
  onShowReads: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Message actions"
          className="h-6 w-6 shrink-0 opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100"
        >
          <MoreVertical className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {onReply && (
          <DropdownMenuItem onSelect={onReply}>
            <MessagesSquare className="mr-2 h-4 w-4" />
            Reply in thread
          </DropdownMenuItem>
        )}
        {onForward && (
          <DropdownMenuItem onSelect={onForward}>
            <Forward className="mr-2 h-4 w-4" />
            Forward
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={onShowReads}>
          <Eye className="mr-2 h-4 w-4" />
          Who has read this
        </DropdownMenuItem>
        {attachments?.map((a) => (
          <DropdownMenuItem key={a.id} onSelect={() => void saveAttachment(a)}>
            <Download className="mr-2 h-4 w-4 shrink-0" />
            <span className="max-w-[14rem] truncate">
              {attachments.length === 1 ? 'Download' : `Download ${a.filename}`}
            </span>
          </DropdownMenuItem>
        ))}
        {onEdit && (
          <DropdownMenuItem
            onSelect={onEdit}
            disabled={!canEdit}
            // Saying why it is unavailable is more use than an item that
            // simply does nothing.
            title={canEdit ? undefined : 'Messages can only be edited for five minutes'}
          >
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </DropdownMenuItem>
        )}
        {canDelete && onDelete && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive" onSelect={onDelete}>
              <Trash2 className="mr-2 h-4 w-4" />
              Delete for everyone
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * One thread, beside the conversation it came out of.
 *
 * Fetched by the root's id, and the socket writes new replies straight into
 * the same cache entry, so a reply arriving while this is open appears here
 * rather than being discovered on a refresh. The whole thread comes back in
 * one request: a thread is one conversation about one message, so it is
 * bounded in a way channel history is not, and paging it would be machinery
 * for a case that does not happen. The server caps it at 200 anyway, and the
 * root's reply count still reports the true total, so a pathological thread
 * shows a gap rather than silently losing its beginning.
 */
function ThreadPanel({
  rootId,
  currentUserId,
  statusOf,
  personOf,
  nameOf,
  onToggleReaction,
  onPatch,
  onSend,
  onClose,
}: {
  rootId: string;
  currentUserId?: string;
  statusOf: (userId: string) => PresenceStatus;
  personOf: (userId: string) => { note?: string } | undefined;
  nameOf: (userId: string) => string | undefined;
  onToggleReaction: (messageId: string, emoji: string, current: ReactionMap | undefined) => void;
  onPatch: (messageId: string, update: (message: Message) => Message) => void;
  onSend: (content: string) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState('');
  const endRef = useRef<HTMLDivElement>(null);
  // A thread is opened to answer in it.
  const replyRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (!typesOnOpen()) return;
    return focusWhenFree(() => replyRef.current);
  }, [rootId]);
  const { data, isLoading, error } = useQuery({
    queryKey: ['thread', rootId],
    queryFn: () => channelsApi.thread(rootId),
  });

  const root = data?.data?.root as Message | undefined;
  const replies = useMemo<Message[]>(() => (data?.data?.replies as Message[]) ?? [], [data]);

  // A thread is read from the top — the question is the point of it — but a
  // reply arriving while you are in it should be visible without scrolling.
  const newestReplyId = replies[replies.length - 1]?.id;
  useEffect(() => {
    if (newestReplyId) endRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [newestReplyId]);

  // An open thread in front of you is read: its replies turn to a double tick
  // for whoever wrote them. A tab in the background has not been read yet.
  useEffect(() => {
    if (!newestReplyId) return;
    const read = () => {
      if (document.visibilityState !== 'visible') return;
      channelsApi.markThreadRead(newestReplyId).catch(() => {
        // Marked again on the next reply, or the next time the thread opens.
      });
    };
    read();
    document.addEventListener('visibilitychange', read);
    return () => document.removeEventListener('visibilitychange', read);
  }, [newestReplyId]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!draft.trim()) return;
    onSend(draft);
    setDraft('');
  };

  return (
    <aside className="absolute inset-0 z-20 flex flex-col bg-background lg:static lg:z-auto lg:w-96 lg:shrink-0 lg:border-l lg:border-border">
      <div className="flex h-16 shrink-0 items-center justify-between border-b border-border px-4">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">Thread</h2>
          <p className="truncate text-xs text-muted-foreground">
            {replies.length === 0 ? 'No replies yet' : replyCountLabel(replies.length)}
          </p>
        </div>
        <Button variant="ghost" size="icon" aria-label="Close thread" onClick={onClose}>
          <X className="h-5 w-5" />
        </Button>
      </div>

      <div className="flex-1 space-y-1 overflow-y-auto p-4">
        {isLoading && <p className="text-sm text-muted-foreground">Loading thread…</p>}
        {error && (
          <p className="text-sm text-muted-foreground">
            {apiErrorMessage(error, 'Could not load that thread.')}
          </p>
        )}
        {root && (
          <>
            {/* The message the thread is about, whatever became of it: a root
                is soft-deleted and keeps its place precisely so the replies
                under it still have a parent, and it renders as the tombstone
                MessageBubble already knows how to draw. */}
            <MessageBubble
              message={root}
              isOwn={root.userId === currentUserId}
              status={statusOf(root.userId)}
              note={personOf(root.userId)?.note}
              grouped={false}
              currentUserId={currentUserId}
              nameOf={nameOf}
              onToggleReaction={onToggleReaction}
              onPatch={onPatch}
            />
            <div className="my-3 flex items-center gap-3">
              <div className="h-px flex-1 bg-border" />
              <span className="text-xs font-medium text-muted-foreground">
                {replies.length === 0 ? 'No replies yet' : replyCountLabel(replies.length)}
              </span>
              <div className="h-px flex-1 bg-border" />
            </div>
            {replies.map((reply, i) => {
              const previous = replies[i - 1];
              const sameAuthor = previous?.userId === reply.userId;
              const closeInTime =
                previous != null &&
                new Date(reply.createdAt).getTime() - new Date(previous.createdAt).getTime() <
                  5 * 60 * 1000;
              return (
                <MessageBubble
                  key={reply.id}
                  message={reply}
                  isOwn={reply.userId === currentUserId}
                  status={statusOf(reply.userId)}
                  note={personOf(reply.userId)?.note}
                  grouped={sameAuthor && closeInTime}
                  currentUserId={currentUserId}
                  nameOf={nameOf}
                  onToggleReaction={onToggleReaction}
                  onPatch={onPatch}
                />
              );
            })}
            <div ref={endRef} />
          </>
        )}
      </div>

      <form onSubmit={submit} className="shrink-0 border-t border-border p-4">
        <div className="flex items-end gap-2">
          <Input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="Reply…"
            aria-label="Reply in thread"
            ref={replyRef}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                submit(e);
              }
            }}
          />
          <Button type="submit" size="icon" disabled={!draft.trim()} className="shrink-0" aria-label="Send reply">
            <Send className="h-5 w-5" />
          </Button>
        </div>
      </form>
    </aside>
  );
}

/** Who has read a message, in the order they read it. */
function ReadByDialog({ messageId, onClose }: { messageId: string | null; onClose: () => void }) {
  const { data, isLoading } = useQuery({
    queryKey: ['message-reads', messageId],
    queryFn: () => channelsApi.messageReads(messageId!),
    enabled: !!messageId,
  });
  const reads = data?.data ?? [];

  return (
    <Dialog open={!!messageId} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Read by</DialogTitle>
          <DialogDescription>
            Everybody in this conversation who has read as far as this message.
          </DialogDescription>
        </DialogHeader>
        {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
        {!isLoading && reads.length === 0 && (
          <p className="text-sm text-muted-foreground">Nobody has read it yet.</p>
        )}
        <ul className="max-h-64 space-y-1 overflow-y-auto">
          {reads.map((r: any) => (
            <li key={r.userId} className="flex items-center justify-between gap-3 text-sm">
              <span className="truncate">{r.name}</span>
              <span className="shrink-0 text-xs text-muted-foreground">
                {formatRelativeTime(r.readAt)}
              </span>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
