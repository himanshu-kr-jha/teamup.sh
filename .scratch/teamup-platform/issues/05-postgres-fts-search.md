## Question

Configure Postgres full-text search for chat/message history: `tsvector` column strategy (generated column vs. trigger), language configuration, ranking (ts_rank_cd), trigram extension (pg_trgm) for fuzzy/prefix search, index maintenance, and query API (search endpoint with pagination, highlighting, filters by channel/user/date).

Status: claimed

## Answer

**Decision: Postgres FTS search architecture defined below.**

### 1. Schema Design

```sql
-- migrations/004_messages_search.sql
CREATE TABLE messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    channel_id      UUID NOT NULL REFERENCES channels(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    content         TEXT NOT NULL,
    content_search  tsvector GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(content, ''))
    ) STORED,
    reply_to        UUID REFERENCES messages(id),
    edited_at       TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- GIN index for FTS (primary search path)
CREATE INDEX idx_messages_content_search ON messages USING GIN (content_search);

-- pg_trgm index for fuzzy/prefix search (fallback)
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_messages_content_trgm ON messages USING GIN (content gin_trgm_ops);

-- Composite indexes for filtered searches
CREATE INDEX idx_messages_org_channel_created ON messages (org_id, channel_id, created_at DESC);
CREATE INDEX idx_messages_org_user_created ON messages (org_id, user_id, created_at DESC);
```

**Why generated column (`STORED`) over trigger?**
- Automatic, no trigger overhead
- Updated on every `INSERT`/`UPDATE` — always consistent
- `STORED` = computed at write time, no read penalty
- PostgreSQL 12+ supports this natively

---

### 2. Language Configuration

**Primary:** `'english'` (built-in) — good for general use, handles stemming (running → run).

**Per-org language (v2):** Add `org_id` → language mapping, use `to_tsvector(org_language, content)`.

**Custom dictionary (v2):** For domain terms (product names, acronyms), create `CREATE TEXT SEARCH DICTIONARY` + `CONFIGURATION`.

---

### 3. Search Query Types

| Query Type | SQL Function | Use Case |
|------------|--------------|----------|
| **Plain** | `plainto_tsquery` | User types "meeting notes" → `'meeting' & 'notes'` |
| **Phrase** | `phraseto_tsquery` | "project alpha" → `'project' <-> 'alpha'` (adjacent) |
| **Web** | `websearch_to_tsquery` | "meeting -notes" → `'meeting' & !'notes'` (Google-style) |
| **Prefix** | `to_tsquery('meet:*')` | Autocomplete "meet" → meets, meeting, meeting's |

**Default:** `websearch_to_tsquery` — supports quotes, minus, wildcards.

---

### 4. Ranking

```sql
-- ts_rank_cd (cover density) — better for short documents like chat messages
SELECT id, content,
       ts_rank_cd(content_search, query, 32) AS rank  -- 32 = rank normalization (doc length)
FROM messages, websearch_to_tsquery('english', 'meeting notes') query
WHERE content_search @@ query
ORDER BY rank DESC, created_at DESC
LIMIT 20;
```

**Rank normalization flags (bitmask):**
- `1` — ignore document length
- `2` — divide by log(document length)
- `4` — divide by log(1 + unique words)
- `8` — divide by 1 + log(unique words)
- `16` — divide by sqrt(document length)
- `32` — **recommended** (combines 1+2+4+8+16 = 31, plus 1 = 32)

---

### 5. Search API Endpoint

```go
// internal/chat/search.go
type SearchRequest struct {
    OrgID       string   `json:"orgId"`
    Query       string   `json:"query"`
    ChannelIDs  []string `json:"channelIds,omitempty"`
    UserIDs     []string `json:"userIds,omitempty"`
    DateFrom    *time.Time `json:"dateFrom,omitempty"`
    DateTo      *time.Time `json:"dateTo,omitempty"`
    Limit       int      `json:"limit,omitempty"`        // default 20, max 100
    Offset      int      `json:"offset,omitempty"`       // for pagination
    Highlight   bool     `json:"highlight,omitempty"`    // default true
}

type SearchResult struct {
    MessageID   string    `json:"messageId"`
    ChannelID   string    `json:"channelId"`
    UserID      string    `json:"userId"`
    UserName    string    `json:"userName"`
    Content     string    `json:"content"`
    Highlighted string    `json:"highlighted,omitempty"` // <mark> wrapped
    Rank        float64   `json:"rank"`
    CreatedAt   time.Time `json:"createdAt"`
}

type SearchResponse struct {
    Results  []SearchResult `json:"results"`
    Total    int64          `json:"total"`
    Limit    int            `json:"limit"`
    Offset   int            `json:"offset"`
    Query    string         `json:"query"`
    TookMS   int64          `json:"tookMs"`
}
```

**Handler:**
```go
func (s *Service) SearchMessages(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
    start := time.Now()

    // Build query
    tsQuery := "websearch_to_tsquery('english', $1)"
    args := []any{req.Query}
    argIdx := 2

    // Base query
    query := `
        SELECT m.id, m.channel_id, m.user_id, u.name, m.content, m.created_at,
               ts_rank_cd(m.content_search, ` + tsQuery + `, 32) AS rank
        FROM messages m
        JOIN users u ON u.id = m.user_id
        WHERE m.org_id = $` + strconv.Itoa(argIdx) + `
          AND m.deleted_at IS NULL
          AND m.content_search @@ ` + tsQuery
    args = append(args, req.OrgID)
    argIdx++

    // Filters
    if len(req.ChannelIDs) > 0 {
        placeholders := make([]string, len(req.ChannelIDs))
        for i := range req.ChannelIDs {
            placeholders[i] = "$" + strconv.Itoa(argIdx)
            args = append(args, req.ChannelIDs[i])
            argIdx++
        }
        query += " AND m.channel_id IN (" + strings.Join(placeholders, ",") + ")"
    }
    if len(req.UserIDs) > 0 {
        // similar
    }
    if req.DateFrom != nil {
        query += " AND m.created_at >= $" + strconv.Itoa(argIdx)
        args = append(args, *req.DateFrom)
        argIdx++
    }
    if req.DateTo != nil {
        query += " AND m.created_at <= $" + strconv.Itoa(argIdx)
        args = append(args, *req.DateTo)
        argIdx++
    }

    // Count total (for pagination)
    countQuery := strings.Replace(query, "SELECT m.id, m.channel_id, m.user_id, u.name, m.content, m.created_at, ts_rank_cd(m.content_search, " + tsQuery + ", 32) AS rank", "SELECT COUNT(*)", 1)
    var total int64
    if err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
        return nil, err
    }

    // Pagination + ordering
    query += " ORDER BY rank DESC, m.created_at DESC"
    if req.Limit <= 0 || req.Limit > 100 { req.Limit = 20 }
    query += " LIMIT $" + strconv.Itoa(argIdx)
    args = append(args, req.Limit)
    argIdx++
    if req.Offset > 0 {
        query += " OFFSET $" + strconv.Itoa(argIdx)
        args = append(args, req.Offset)
    }

    rows, err := s.db.Query(ctx, query, args...)
    if err != nil { return nil, err }
    defer rows.Close()

    var results []SearchResult
    for rows.Next() {
        var r SearchResult
        if err := rows.Scan(&r.MessageID, &r.ChannelID, &r.UserID, &r.UserName, &r.Content, &r.CreatedAt, &r.Rank); err != nil {
            return nil, err
        }
        if req.Highlight {
            r.Highlighted = s.highlight(r.Content, req.Query)
        }
        results = append(results, r)
    }

    return &SearchResponse{
        Results: results, Total: total, Limit: req.Limit, Offset: req.Offset,
        Query: req.Query, TookMS: time.Since(start).Milliseconds(),
    }, nil
}
```

---

### 6. Highlighting (ts_headline)

```go
func (s *Service) highlight(content, query string) string {
    // Use ts_headline for proper highlighting with ranking context
    // Fallback to simple regex if ts_headline too slow
    sql := `
        SELECT ts_headline('english', $1, websearch_to_tsquery('english', $2),
            'StartSel=<mark>, StopSel=</mark>, MaxWords=35, MinWords=15, ShortWord=3, HighlightAll=TRUE')
    `
    var highlighted string
    if err := s.db.QueryRow(context.Background(), sql, content, query).Scan(&highlighted); err != nil {
        // Fallback: simple case-insensitive wrap
        return simpleHighlight(content, query)
    }
    return highlighted
}
```

**ts_headline options:**
- `StartSel=<mark>, StopSel=</mark>` — HTML tags
- `MaxWords=35` — snippet length
- `MinWords=15` — minimum context
- `ShortWord=3` — ignore words ≤3 chars
- `HighlightAll=TRUE` — highlight all occurrences

---

### 7. Fuzzy / Prefix Search (pg_trgm)

For "did you mean?" and autocomplete:

```sql
-- Fuzzy search (similarity > 0.3)
SELECT content, similarity(content, 'meeting') AS sim
FROM messages
WHERE org_id = $1 AND content % 'meeting'  -- % = similarity operator
ORDER BY sim DESC
LIMIT 10;

-- Prefix autocomplete (trigram index)
SELECT DISTINCT content
FROM messages
WHERE org_id = $1 AND content LIKE 'meet%'
ORDER BY created_at DESC
LIMIT 10;
```

**pg_trgm settings:**
```sql
-- Lower threshold = more results, slower
SET pg_trgm.similarity_threshold = 0.3;  -- default 0.3
-- For prefix, use `word_similarity` (PostgreSQL 14+)
```

**API endpoint:** `GET /api/search/suggest?q=meet&limit=5` → returns top matching phrases.

---

### 8. Index Maintenance

| Operation | Strategy |
|-----------|----------|
| **INSERT/UPDATE** | Automatic (generated column) |
| **Bulk load** | `CREATE INDEX CONCURRENTLY` after load |
| **Reindex** | `REINDEX INDEX CONCURRENTLY idx_messages_content_search` (monthly cron) |
| **Bloat control** | `pg_stat_clear_snapshot()`, monitor `pg_stat_user_indexes` |
| **Partitioning (v2)** | Partition `messages` by `created_at` (monthly) — each partition has own GIN index |

**Monitoring:**
```sql
-- Index size
SELECT pg_size_pretty(pg_relation_size('idx_messages_content_search'));

-- Index usage
SELECT * FROM pg_stat_user_indexes WHERE indexrelname = 'idx_messages_content_search';
```

---

### 9. Performance Targets (v1)

| Metric | Target |
|--------|--------|
| **Search latency (p95)** | < 200ms for 10M messages |
| **Index size** | ~30% of table size (GIN) |
| **Write overhead** | < 5% latency increase |
| **Concurrent searches** | 100+/sec per node |

**Optimization knobs:**
- `work_mem = 256MB` (for sort/rank)
- `maintenance_work_mem = 1GB` (for index build)
- `gin_pending_list_limit = 4MB` (flush frequency)

---

### 10. Open Questions (for later)

- **Semantic search** (pgvector + embeddings) — v2
- **Cross-org search** (federation) — v2
- **Search analytics** (popular queries, no-results) — v2
- **Real-time search** (pg_notify + materialized view) — v2

Status: resolved
