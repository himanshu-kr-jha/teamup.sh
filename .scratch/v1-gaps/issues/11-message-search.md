# 11 — Message search

**What to build:** The search box in the header finds messages.

It is decorative today, and the endpoint the frontend expects does not exist on the server — this is unbuilt, not merely unwired. The groundwork is already in place: the messages table carries a generated full-text column with a GIN index, and the platform spec settled the query approach (Google-style parsing, relevance ranking, highlighted snippets).

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Typing a query returns matching messages, best match first
- [ ] Results show the channel, the author, when it was sent, and a snippet with the match highlighted
- [ ] Quoted phrases and negated terms behave the way a search box is expected to
- [ ] A result links through to that message in its channel
- [ ] Results can be narrowed to a channel, a person, or a date range
- [ ] Results only ever include channels the searcher can read, and never another organization's messages
- [ ] Results are paged rather than returning everything
- [ ] No matches shows an empty state; a failure says so
