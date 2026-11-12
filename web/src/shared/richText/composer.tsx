import {
  forwardRef,
  useCallback,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  type ClipboardEvent,
  type KeyboardEvent,
  type RefObject,
  type TextareaHTMLAttributes,
} from 'react';
import { Bold, Code, Italic, List, ListOrdered, Quote, Strikethrough } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { htmlToChatText, toggleLinePrefix, toggleMark, type Edit } from './format';

export type FormatKind = 'bold' | 'italic' | 'strike' | 'code' | 'bullet' | 'numbered' | 'quote';

const MARK: Partial<Record<FormatKind, string>> = { bold: '*', italic: '_', strike: '~', code: '`' };

/**
 * Writing formatted text in a text box: pasting formatted text keeps its
 * formatting, Ctrl+B, Ctrl+I and Ctrl+Shift+X format the selection, and the
 * box grows with what is written, up to maxHeight.
 *
 * Changes go in as typing would, so Ctrl+Z takes them back.
 */
export function useFormatting(
  ref: RefObject<HTMLTextAreaElement>,
  value: string,
  setValue: (text: string) => void,
  { maxHeight = 200 }: { maxHeight?: number } = {}
) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight + (el.offsetHeight - el.clientHeight), maxHeight)}px`;
    el.style.overflowY = el.scrollHeight > maxHeight ? 'auto' : 'hidden';
  }, [ref, value, maxHeight]);

  const put = useCallback(
    (edit: Edit, replaceFrom: number, replaceTo: number, inserted: string) => {
      const el = ref.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(replaceFrom, replaceTo);
      // insertText keeps the browser's undo; where it is missing the text is set outright.
      const typed = typeof document.execCommand === 'function' && document.execCommand('insertText', false, inserted);
      if (!typed || el.value !== edit.text) setValue(edit.text);
      requestAnimationFrame(() => el.setSelectionRange(edit.start, edit.end));
    },
    [ref, setValue]
  );

  const apply = useCallback(
    (kind: FormatKind) => {
      const el = ref.current;
      if (!el) return;
      const { selectionStart: s, selectionEnd: e } = el;
      const text = el.value;
      const mark = MARK[kind];
      const edit =
        mark !== undefined
          ? toggleMark(text, s, e, kind === 'code' && text.slice(s, e).includes('\n') ? '```' : mark)
          : toggleLinePrefix(text, s, e, kind as 'bullet' | 'numbered' | 'quote');
      // Only the part that changed is replaced, so undo puts back just that.
      let from = 0;
      while (from < text.length && from < edit.text.length && text[from] === edit.text[from]) from++;
      let tail = 0;
      while (
        tail < text.length - from &&
        tail < edit.text.length - from &&
        text[text.length - 1 - tail] === edit.text[edit.text.length - 1 - tail]
      )
        tail++;
      put(edit, from, text.length - tail, edit.text.slice(from, edit.text.length - tail));
    },
    [ref, put]
  );

  const onPaste = useCallback(
    (e: ClipboardEvent<HTMLTextAreaElement>) => {
      const html = e.clipboardData.getData('text/html');
      if (!html) return;
      const converted = htmlToChatText(html);
      const plain = e.clipboardData.getData('text/plain');
      if (!converted || converted === plain.trim()) return;
      e.preventDefault();
      const el = e.currentTarget;
      const { selectionStart: s, selectionEnd: end, value: text } = el;
      const next = text.slice(0, s) + converted + text.slice(end);
      const caret = s + converted.length;
      put({ text: next, start: caret, end: caret }, s, end, converted);
    },
    [put]
  );

  /** Formatting keys; true if the key was one, and is dealt with. */
  const onFormatKey = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>): boolean => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey) return false;
      const key = e.key.toLowerCase();
      const kind: FormatKind | null =
        key === 'b' && !e.shiftKey ? 'bold' : key === 'i' && !e.shiftKey ? 'italic' : key === 'x' && e.shiftKey ? 'strike' : key === 'e' && !e.shiftKey ? 'code' : null;
      if (!kind) return false;
      e.preventDefault();
      apply(kind);
      return true;
    },
    [apply]
  );

  return { apply, onPaste, onFormatKey };
}

const BUTTONS: Array<{ kind: FormatKind; label: string; keys?: string; Icon: typeof Bold }> = [
  { kind: 'bold', label: 'Bold', keys: 'Ctrl+B', Icon: Bold },
  { kind: 'italic', label: 'Italic', keys: 'Ctrl+I', Icon: Italic },
  { kind: 'strike', label: 'Strikethrough', keys: 'Ctrl+Shift+X', Icon: Strikethrough },
  { kind: 'code', label: 'Code', keys: 'Ctrl+E', Icon: Code },
  { kind: 'bullet', label: 'Bulleted list', Icon: List },
  { kind: 'numbered', label: 'Numbered list', Icon: ListOrdered },
  { kind: 'quote', label: 'Quote', Icon: Quote },
];

/** The formatting buttons, over a message box. */
export function FormatBar({ onFormat, className }: { onFormat: (kind: FormatKind) => void; className?: string }) {
  return (
    <div role="toolbar" aria-label="Formatting" className={cn('flex flex-wrap items-center gap-0.5', className)}>
      {BUTTONS.map(({ kind, label, keys, Icon }) => (
        <button
          key={kind}
          type="button"
          aria-label={label}
          title={keys ? `${label} (${keys})` : label}
          // Before the box loses focus, so what is selected in it stays selected.
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onFormat(kind)}
          className="flex h-7 w-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Icon className="h-4 w-4" aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}

/**
 * A text box for a message: Enter is up to onKeyDown, Shift+Enter is a new
 * line, and pasting and the formatting keys work as in the message box.
 */
export const FormattedTextarea = forwardRef<
  HTMLTextAreaElement,
  Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'value' | 'onChange'> & {
    value: string;
    onValueChange: (text: string) => void;
    maxHeight?: number;
  }
>(function FormattedTextarea({ value, onValueChange, onKeyDown, maxHeight = 160, className, ...rest }, outer) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useImperativeHandle(outer, () => ref.current!, []);
  const formatting = useFormatting(ref, value, onValueChange, { maxHeight });
  return (
    <textarea
      rows={1}
      {...rest}
      ref={ref}
      value={value}
      onChange={(e) => onValueChange(e.target.value)}
      onPaste={formatting.onPaste}
      onKeyDown={(e) => {
        if (formatting.onFormatKey(e)) return;
        onKeyDown?.(e);
      }}
      className={cn(
        'flex w-full resize-none rounded-md border border-input bg-background px-3 py-1.5 text-sm leading-6 ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50',
        className
      )}
    />
  );
});
