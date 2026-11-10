import { useMemo, useRef, useState, type ComponentProps } from 'react';
import { Textarea } from '@/shared/components/ui/textarea';
import { useAuth } from '@/features/auth/AuthProvider';
import { insertMention, mentionQuery, suggestPeople, type Mentionable } from './mentions';
import { MentionSuggestions, useMentionables } from './MentionUI';

/**
 * A text box where typing @ offers people to name, as the chat message box
 * does. Whoever is named is told by the server; this only helps write it.
 */
export function MentionTextarea({
  value,
  onValueChange,
  onKeyDown,
  ...rest
}: Omit<ComponentProps<typeof Textarea>, 'value' | 'onChange'> & {
  value: string;
  onValueChange: (value: string) => void;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const { user } = useAuth();
  const people = useMentionables();
  const [mention, setMention] = useState<{ query: string; start: number } | null>(null);
  const [active, setActive] = useState(0);
  const choices = useMemo(
    () => (mention ? suggestPeople(mention.query, people.filter((p) => p.id !== user?.id)) : []),
    [mention, people, user?.id]
  );

  const follow = (text: string, caret: number) => {
    const next = mentionQuery(text, caret);
    if (next?.query !== mention?.query) setActive(0);
    setMention(next);
  };
  const pick = (person: Mentionable) => {
    if (!mention) return;
    const caret = ref.current?.selectionStart ?? value.length;
    const next = insertMention(value, mention.start, caret, person);
    onValueChange(next.text);
    setMention(null);
    requestAnimationFrame(() => {
      ref.current?.focus();
      ref.current?.setSelectionRange(next.caret, next.caret);
    });
  };

  return (
    <div className="relative">
      {choices.length > 0 && (
        <MentionSuggestions people={choices} active={Math.min(active, choices.length - 1)} onPick={pick} onHover={setActive} />
      )}
      <Textarea
        {...rest}
        ref={ref}
        value={value}
        aria-autocomplete="list"
        aria-expanded={choices.length > 0}
        onChange={(e) => {
          onValueChange(e.target.value);
          follow(e.target.value, e.target.selectionStart ?? e.target.value.length);
        }}
        onSelect={(e) => follow(e.currentTarget.value, e.currentTarget.selectionStart ?? 0)}
        onBlur={() => setMention(null)}
        onKeyDown={(e) => {
          if (choices.length > 0) {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
              e.preventDefault();
              const step = e.key === 'ArrowDown' ? 1 : -1;
              setActive((i) => (i + step + choices.length) % choices.length);
              return;
            }
            if (e.key === 'Enter' || e.key === 'Tab') {
              e.preventDefault();
              pick(choices[Math.min(active, choices.length - 1)]);
              return;
            }
            if (e.key === 'Escape') {
              e.preventDefault();
              e.stopPropagation();
              setMention(null);
              return;
            }
          }
          onKeyDown?.(e);
        }}
      />
    </div>
  );
}
