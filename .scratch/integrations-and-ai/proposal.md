# Integrations and AI for TeamUp: a proposal

Status: proposal for the owner to decide on.
Date: 8 November 2026.
Scope: research and design only; no code was changed.

## The goal in one paragraph

The owner wants "a Slack with AI capabilities".
Each person should be able to manage their own work from a laptop or a phone.
The organization should be able to see how its people and teams are doing.
Code hosts such as GitHub and Bitbucket should plug in at two levels: the organization connects its repositories, and each member connects their own account, so commits and pull requests are attributed to real people and the team can talk about code inside TeamUp.

This document answers "how do we integrate third-party apps" and "what else can we do", and turns the answer into a phased plan tied to the packages that already exist.

## Recommendation in brief

1. Build a small integrations foundation first: encrypted secret storage, a durable job queue in Postgres, bot users, human-readable task keys, and the audit log that already has a table but no writers.
2. Then build GitHub as a GitHub App that each TeamUp server registers for itself with GitHub's manifest flow, with per-member account linking.
   The first features are pull request, review and CI notifications into channels, one chat thread per pull request, link previews for GitHub URLs, and linking pull requests and commits to TeamUp tasks through a task key such as `TU-142`.
3. In parallel, ship the cheapest AI win: turning meeting-minute action items into tasks, since the note-taker already extracts them with owners and due dates.
4. Next, lift the note-taker's model client into a shared AI service and build catch-up summaries, a daily briefing, standup drafts and an assistant that answers only from what the asking person can already see.
5. Frame "monitoring" as delivery and team-health dashboards built on the task dashboard's existing self/team/org scoping, with every person able to see exactly what their manager sees about them.
6. Add Bitbucket Cloud and GitLab behind the same provider abstraction once GitHub is solid, and calendar sync with Google and Outlook once OIDC sign-in is real.

## What TeamUp already has to build on

These facts come from reading the code, and they shape the design.

| Existing piece | Where | Why it matters here |
| --- | --- | --- |
| Org configuration with an `is_secret` flag | `org_configs` table, `internal/org/org.go` `SetConfig` | Natural home for integration settings, but see the warning below: secrets are flagged, not encrypted. |
| Linked identities | `linked_identities` table (user, provider, provider id, tokens) | Shape fits per-user GitHub linking, but tokens are stored as plain text columns. |
| LLM providers for minutes | `internal/notetaker/providers.go` and `settings.go`: Claude, OpenAI, NVIDIA, any OpenAI-compatible endpoint | A tested `Writer` interface to lift into a shared `internal/ai` package. |
| Minutes with structured action items | `internal/notetaker/minutes.go`: `ActionItem{Owner, Task, Due}` | Action items can become tasks in one click with little new code. |
| Sarvam speech to text | `internal/notetaker` | Voice messages from the phone can become tasks or questions to the assistant. |
| Permission-scoped search | `internal/chat` search over `channel_members`, Postgres full text plus trigram | The retrieval layer for "ask your workspace" already enforces who can see what. |
| Read markers | `channel_members.last_read_at` | Defines "what did I miss" exactly for catch-up summaries. |
| Message kinds | `messages.kind` (`user`, call events) from migration `0009` | Integration and bot posts can be a new kind and render differently. |
| Dashboard scoping | `internal/tasks/dashboard.go` `AnswerableFor`: self, team or org | The rule for what a manager may see is already written once; reuse it for code and meeting metrics. |
| Contributions | `internal/tasks/contributions.go`: completed, involved, open, added-to per person | The start of a delivery dashboard. |
| Scrums | `internal/tasks/scrum.go` | Where an AI-drafted standup lands. |
| Standing meetings and attendance | `internal/calls/standing.go` `Attendance` | Meeting attendance is already tracked from LiveKit webhooks. |
| Calendar | `internal/calendar` | Briefings and workload signals need meeting hours. |
| Notifications and web push | `internal/notifications` `Notify`, `NotifyMany` | Delivery path for integration alerts and the daily briefing on the phone. |
| Audit log table | `audit_logs` in migration `0001` | Exists, but nothing writes to it yet. |
| Webhook receiving pattern | `internal/calls/webhook.go` for LiveKit | Same shape as GitHub and Bitbucket webhooks: verify, record, act. |
| Multi-replica real-time | `internal/websocket/fanout.go` over Redis | Integration posts reach every replica through the existing broadcast path. |

Gaps that matter for this proposal:

- **Secrets are not encrypted at rest.**
  `SetConfig` writes the JSON value as-is; `is_secret` only keeps it out of `ListPublicConfigs`.
  The note-taker's LLM and speech keys are therefore readable by anyone with database access or a backup.
  The JWT signing key, by contrast, is encrypted with `TEAMUP_AUTH_SECRET`, so the pattern exists.
- **`linked_identities` stores OAuth access and refresh tokens as plain text.**
- **Tasks have no short human key.**
  `tasks.id` is a UUID, so nobody can type a task into a branch name or commit message today.
- **No job queue.**
  `docs/ARCHITECTURE.md` says so on purpose, and background work today runs in goroutines and tickers.
  Webhook processing, retries, embeddings and scheduled briefings need work that survives a restart and runs once across replicas.
- **No slash commands, no bot users, no incoming webhooks.**
- **OIDC sign-in is a stub** (`internal/auth/routes.go` `findOrCreateUser`), which blocks "Sign in with Google" and makes Google Workspace and Microsoft 365 integrations harder than they need to be.
- `docs/ARCHITECTURE.md` still lists "AI features" under "Not built at all" and its package table omits `internal/notetaker`; it should be refreshed when this work starts.

---

## 1. GitHub

### 1.1 GitHub App, not OAuth App

GitHub offers two kinds of integration, and GitHub's own guidance prefers GitHub Apps for new work.

| | GitHub App | OAuth App |
| --- | --- | --- |
| Who acts | The app itself, as an installation, plus optionally a user | Always a user |
| Permissions | Fine-grained, per repository, chosen by the org admin at install time | Broad scopes such as `repo`, which grant every repository the user can see |
| Repositories | The admin picks all repos or a list | Everything the user can reach |
| Survives a person leaving | Yes, the installation belongs to the org | No, tokens die with the user |
| Webhooks | One app-level webhook for every installation | Must be configured per repository or org by hand |
| Rate limit | Grows with repos and users (below) | Shared with the user's own 5,000 per hour |

Use a GitHub App.
It gives an org-level installation (the organization's repos come in) and user-to-server tokens (each member links their own account) in one registration.
See [Differences between GitHub Apps and OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps).

### 1.2 Self-hosted: every TeamUp server registers its own App

TeamUp is self-hosted, so there is no central "TeamUp app" on GitHub that every customer installs.
Each server registers its own App, and GitHub supports exactly this through the [manifest flow](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest):

1. An admin opens Admin, Integrations, GitHub and presses "Create GitHub App".
2. TeamUp posts a manifest to GitHub naming the app, its permissions, its events, its webhook URL (`https://<teamup-host>/api/integrations/github/webhook`), its callback URL, and requesting expiring user tokens.
3. The admin confirms on GitHub, choosing their organization as the owner.
4. GitHub redirects back with a temporary code, and TeamUp calls `POST /app-manifests/{code}/conversions`.
5. The response contains the App id, private key (PEM), webhook secret, client id and client secret, which TeamUp stores encrypted.
6. TeamUp sends the admin to the App's install page, where they pick all repositories or a list.

This removes the copy-paste of keys that makes self-hosted integrations fragile.
The App should be owned by the customer's GitHub organization, not a personal account, so it survives people leaving.
GitHub allows up to 100 Apps per owner, which is no constraint here ([Registering a GitHub App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app)).

**Webhooks need a public HTTPS address.**
The live server on EC2 is reachable, so this is fine there.
A TeamUp running only on a LAN cannot receive webhooks; the choice is either to document that, to fall back to polling, or to use a relay.
This is a decision for the owner (see the decisions list).

### 1.3 GitHub Enterprise Server

GitHub Enterprise Server (GHES) supports GitHub Apps and the manifest flow, and each GHES instance must register its own App, which is the same model as above ([Making your GitHub App available for GHES](https://docs.github.com/github-ae@latest/apps/sharing-github-apps/making-your-github-app-available-for-github-enterprise-server); current docs go up to [GHES 3.22](https://docs.github.com/en/enterprise-server@3.22/apps/sharing-github-apps)).
The only difference for TeamUp is the base URLs: `https://<ghes-host>` for the web flow and `https://<ghes-host>/api/v3` for the API.
Make the base URL a setting from day one, defaulting to `github.com`, and GHES support is nearly free.

### 1.4 Permissions to request

Ask for the least that the features need, read-only at first.

| Permission | Level | Used for |
| --- | --- | --- |
| Metadata | Read (mandatory) | Repository list, names |
| Contents | Read | Commits, compare, file snippets for code previews |
| Pull requests | Read (Write only if two-way comments are chosen) | PR state, reviewers, diffs for summaries |
| Issues | Read | Issue previews and notifications |
| Checks, Commit statuses, Actions | Read | CI results and workflow runs |
| Members (organization) | Read | Matching GitHub org members to TeamUp people |
| Email addresses (account) | Read, user token only | Matching commit author emails to the person who linked their account |

Webhook events to subscribe to: `push`, `pull_request`, `pull_request_review`, `pull_request_review_comment`, `issues`, `issue_comment`, `check_suite`, `check_run`, `workflow_run`, `status`, `installation`, `installation_repositories`, `repository`, `member`, `organization`.

### 1.5 Webhooks done properly

- Verify `X-Hub-Signature-256` (HMAC-SHA256 of the raw body with the webhook secret) with a constant-time compare, before parsing ([Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)).
- Answer 2xx within 10 seconds or GitHub counts the delivery as failed, so store and enqueue, then process in the background ([Best practices for webhooks](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)).
- Deduplicate on `X-GitHub-Delivery` with a unique key in a `webhook_deliveries` table.
- GitHub does not retry failed deliveries on its own; a periodic job should list recent failed deliveries through the API and ask for redelivery ([Handling failed webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries)).
- Pin the REST API version header; GitHub's docs now default to `X-GitHub-Api-Version: 2026-03-10` ([Authenticating as an installation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation)).

### 1.6 Rate limits

From [Rate limits for the REST API](https://docs.github.com/rest/using-the-rest-api/rate-limits-for-the-rest-api) and [GraphQL limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api):

- An installation token gets 5,000 requests per hour, plus 50 per hour for each repository above 20 and each org user above 20, capped at 12,500.
- On GitHub Enterprise Cloud an installation gets 15,000 REST requests per hour.
- A user token gets the user's own 5,000 per hour.

For a team of tens of people this is plenty, if TeamUp follows three rules.
Drive everything from webhooks and only call the API to enrich (for example a PR diff for a summary).
Cache what it fetches, keyed by the object and its `updated_at`.
Use conditional requests with ETags, since a `304 Not Modified` does not count against the limit.

### 1.7 Linking people: GitHub users to TeamUp members

Two kinds of identity have to meet: a TeamUp user, and a GitHub account that shows up as a PR author, reviewer, pusher or commit author.

1. **Explicit linking (primary).**
   Each member presses "Connect GitHub" in Settings.
   This is the GitHub App's user authorization flow, which returns a user token that expires after 8 hours with a refresh token valid for 6 months ([Refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens)).
   TeamUp records the numeric GitHub user id (stable, unlike the login, which can be renamed) and the login.
   With the Email addresses permission it also records the person's verified emails.
2. **Commit authors.**
   A commit carries an author email, and GitHub adds `author.login` when the email belongs to an account.
   Match first by GitHub user id, then by any verified email on a linked account, then by the `ID+login@users.noreply.github.com` pattern.
3. **Suggested matches for the unlinked.**
   When an org member is not linked, suggest a match by TeamUp email, and let an admin confirm it.
   Never auto-attribute on a guessed match, because attribution feeds dashboards.
4. **Unmatched activity** is shown with the GitHub login and a "Not a TeamUp member" marker, never dropped silently.

What a user token buys beyond identity: actions taken from TeamUp (a PR comment, a review approval) are authored by that person on GitHub, not by a bot.

### 1.8 Features

**Channel subscriptions.**
`/github subscribe acme/api pulls reviews checks` in a channel, or the same from a settings dialog.
Filters by event type, branch (for example only `main`), and label.
Stored in a `channel_subscriptions` table keyed by channel and repository.

**One thread per pull request.**
When a PR opens, TeamUp posts one root message in each subscribed channel: title, author (as the TeamUp person when linked), branch, linked task, CI state, reviewers.
Later events (review requested, approved, changes requested, CI failed, merged) update the root message's card and add a short reply in its thread.
The thread becomes the place to discuss the PR, so the channel itself stays readable.
This builds on threaded replies, which the chat already supports.

**Personal notifications.**
"Your review was requested", "your PR was approved", "CI failed on your PR", sent through `internal/notifications` to the person, including web push on the phone.
This is often more valuable than channel posts, and it is where Slack's own GitHub app earns its keep.

**Link previews (unfurls).**
`web/src/features/chat/links.ts` already finds URLs in messages on the client.
Add server-side unfurling when a message is sent: recognise `github.com/<owner>/<repo>/pull/<n>`, `/issues/<n>`, `/commit/<sha>`, and `/blob/<ref>/<path>#L10-L24`.
Fetch with the installation token, store the result as a card on the message, and broadcast the edit.
A blob link with a line range renders the code snippet with syntax highlighting, which is the "discuss a code snippet" feature in its simplest form.
Rule: only unfurl repositories this organization installed the App on; a link to anything else stays a plain link.
Private-repo content shown in a channel becomes visible to that channel's members, so the policy needs an owner decision (see decisions).

**Tasks linked to code.**
This needs human task keys first: an org prefix such as `TU` and a per-org sequence, giving `TU-142`.
Then:

- A branch named `tu-142-fix-login`, a PR title containing `TU-142`, or a commit message containing it links that PR or commit to the task.
- The task page gets a "Code" panel listing linked PRs with state and CI status, and the activity log records "PR #87 opened by Asha".
- `Fixes TU-142` in a merged PR can move the task to done, as a per-org setting that is off by default.
- From a task, "Create branch" copies a suggested branch name.

**Discussing a PR inside TeamUp.**
In a PR thread, a reply can carry "Also post on GitHub", which posts it as a PR comment with the author's own user token.
GitHub review comments made on the PR come back into the thread.
This two-way sync needs Pull requests: Write and careful loop prevention (ignore events caused by TeamUp's own posts), so it belongs in a later slice.

**Slash commands.**
TeamUp has none yet, so this is part of the framework in section 3.
Start with `/github subscribe`, `/github unsubscribe`, `/github prs` (my open PRs and reviews waiting on me), and `/task TU-142` (preview a task).

**AI on code.**
A PR summary posted in the PR thread, written from the title, description and diff stats (section 4).

---

## 2. Bitbucket Cloud, GitLab, and one model for all three

### 2.1 Bitbucket Cloud in 2026

Bitbucket's app models changed a lot in the last eighteen months.

- **App passwords are gone.**
  Creation stopped on 9 September 2025 and existing app passwords stopped working on 9 June 2026; integrations must use API tokens or OAuth ([Atlassian announcement](https://www.atlassian.com/blog/bitbucket/bitbucket-cloud-transitions-to-api-tokens-enhancing-security-with-app-password-deprecation)).
- **Atlassian Connect is ending.**
  Connect apps can no longer be updated since 31 March 2026, and Connect support for Bitbucket Cloud ends on 31 January 2027 ([Bitbucket Connect end of support](https://community.developer.atlassian.com/t/bitbucket-cloud-announcing-connect-end-of-support-timeline-and-next-steps/98760), [migration responsibilities](https://community.developer.atlassian.com/t/bitbucket-cloud-connect-end-of-support-understanding-app-migration-responsibilities/103316)).
- **Forge is the replacement, but it is Atlassian-hosted.**
  A Forge app's code runs on Atlassian's infrastructure and is distributed centrally, which fits a SaaS vendor, not thousands of independent self-hosted servers.

So for a self-hosted TeamUp the practical Bitbucket model is:

- **Org level:** a workspace admin creates an OAuth 2.0 consumer in the Bitbucket workspace and pastes its key and secret into TeamUp ([Use OAuth on Bitbucket Cloud](https://support.atlassian.com/bitbucket-cloud/docs/use-oauth-on-bitbucket-cloud/)).
  There is no equivalent of GitHub's manifest flow, so TeamUp should show a short checklist with the exact callback URL and scopes to tick.
  Workspace access tokens are an alternative for server-to-server calls where the plan includes them ([Workspace access tokens](https://support.atlassian.com/bitbucket-cloud/docs/using-workspace-access-tokens/)).
- **Webhooks:** TeamUp creates one workspace-level webhook through the API with a secret, and verifies the `X-Hub-Signature` HMAC that Bitbucket sends when a secret is set ([Workspaces API](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-workspaces/), [Manage webhooks](https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/)).
  Events of interest: `repo:push`, `pullrequest:created`, `pullrequest:updated`, `pullrequest:approved`, `pullrequest:changes_request_created`, `pullrequest:fulfilled`, `pullrequest:comment_created`, `repo:commit_status_updated` (Pipelines and other CI).
- **Per user:** each member authorizes the same OAuth consumer, and TeamUp records their Atlassian `account_id` and Bitbucket UUID.
  Commit authors arrive as a raw `Name <email>` string plus a `user` object when Bitbucket has mapped it, so the same matching order applies as for GitHub.

Bitbucket Data Center (self-managed) is a separate product with its own REST API and webhooks; treat it as a later provider only if a customer asks.

### 2.2 GitLab, briefly

- **Org level:** an OAuth application (instance-wide or on a group) for user linking, and a group access token for server-to-server reads ([Group access tokens](https://docs.gitlab.com/api/group_access_tokens/), [REST API authentication](https://docs.gitlab.com/api/rest/authentication/)).
- **Webhooks:** project webhooks on every tier, group webhooks on paid tiers.
  GitLab authenticates a delivery with a shared secret in the `X-Gitlab-Token` header rather than an HMAC of the body, so the receiver compares the header in constant time.
- **Per user:** OAuth 2.0 with PKCE and refresh tokens ([OAuth 2.0 provider API](https://docs.gitlab.com/api/oauth2/)).
- Self-managed GitLab is common in companies that self-host, so the base URL is a setting, as for GHES.

### 2.3 How the three differ

| | GitHub | Bitbucket Cloud | GitLab |
| --- | --- | --- | --- |
| Org-level identity | GitHub App installation | OAuth consumer or workspace access token | Group access token |
| Self-hosted registration | Manifest flow, nearly one click | Manual consumer creation | Manual OAuth app and token |
| Webhook auth | HMAC-SHA256 header | HMAC header when a secret is set | Shared secret header |
| Webhook setup | Automatic with the App | TeamUp creates it via API | TeamUp creates it via API |
| Stable user id | Numeric user id | `account_id` and UUID | Numeric user id |
| PR name | Pull request | Pull request | Merge request |
| CI | Checks, statuses, Actions | Commit statuses, Pipelines | Pipelines, commit statuses |

### 2.4 One provider abstraction

Add `internal/integrations/scm` with a provider interface and normalized types, so chat, tasks, dashboards and AI never see provider-specific payloads.

```go
type Provider interface {
    Kind() string // "github", "bitbucket", "gitlab"
    VerifyWebhook(h http.Header, body []byte, secret []byte) error
    ParseEvent(h http.Header, body []byte) ([]Event, error)
    UserAuthURL(state string) string
    ExchangeUserCode(ctx context.Context, code string) (Account, Token, error)
    PullRequest(ctx context.Context, repo RepoRef, number int) (*PullRequest, error)
    Commit(ctx context.Context, repo RepoRef, sha string) (*Commit, error)
    FileLines(ctx context.Context, repo RepoRef, ref, path string, from, to int) (string, error)
}
```

Normalized events: `PushEvent`, `PullRequestEvent` (opened, updated, review requested, approved, changes requested, merged, closed), `ReviewCommentEvent`, `CheckEvent` (CI started, passed, failed), `IssueEvent`.
Each carries an `Actor{Provider, ExternalID, Login, Email}` that the identity resolver turns into a TeamUp user id where it can.

Suggested tables (all with `org_id`, and tested with a second org, per the project's own rule):

| Table | Holds |
| --- | --- |
| `scm_connections` | Org, provider, base URL, installation or workspace id, status, encrypted credentials |
| `scm_repositories` | Repos the org connected, with visibility setting |
| `scm_accounts` | TeamUp user to provider account (external id, login, verified emails, encrypted tokens) |
| `scm_events` | Normalized events with resolved TeamUp actor, for dashboards and AI |
| `scm_links` | Task to PR or commit links |
| `channel_subscriptions` | Channel, repository, event filters |
| `webhook_deliveries` | Provider, delivery id (unique), received, processed, error, for dedupe and replay |

---

## 3. A general integrations framework

GitHub should be the first user of a general framework, not a one-off, so the next integrations are cheap.

### 3.1 Foundations

**Encrypted secret storage.**
Add an envelope-encryption helper that derives a key from `TEAMUP_AUTH_SECRET` with HKDF and seals values with AES-256-GCM, the same root secret that already protects `signing_keys`.
Use it for every integration credential and migrate the note-taker keys and `linked_identities` tokens into it.
Keep `is_secret` for the "never send to a page" rule, which stays true.
Document key rotation (re-encrypt under a new key id) before the first customer stores a GitHub private key.

**Durable jobs.**
Add a Postgres-backed queue, for example [River](https://riverqueue.com/) (built on pgx v5, which TeamUp already uses) or a small `jobs` table claimed with `FOR UPDATE SKIP LOCKED`.
This keeps the "no extra service" principle, works across replicas, and gives retries with backoff for webhook processing, unfurls, AI jobs and scheduled briefings.

**Audit log writes.**
Start writing `audit_logs` for integration installs, credential changes, AI admin settings, and every time a manager opens an individual's dashboard (section 5).

### 3.2 Bot users and integration messages

- Add `users.kind` with values `person` and `bot`, so a bot has a name, avatar and channel memberships but cannot sign in.
- Post integration messages with `messages.kind = 'integration'` and a structured `card` payload (title, fields, buttons, colour), rendered by one generic card component in the chat.
- Bots are excluded from seat counts, presence, mentions autocomplete by default, and people dashboards.

### 3.3 Incoming webhooks

A channel admin creates an incoming webhook and gets a secret URL such as `https://<host>/hooks/in/<token>`.
Anything that can send JSON can post into the channel: Sentry alerts, Grafana and Alertmanager, Jenkins, cron jobs, scripts.
Accept a Slack-compatible body (`text`, plus a simple subset of `blocks` or `attachments`) so the many tools that can already "send to a Slack webhook" work unchanged.
This is the highest value-for-effort integration in this whole document, because it unlocks dozens of tools without writing any of them.

### 3.4 Outgoing webhooks and an app model

- **Outgoing webhooks:** an admin registers a URL for events such as `message.created` in a channel, `task.created`, `task.status_changed`.
  Deliveries are signed with HMAC-SHA256, retried by the job queue, and recorded for replay.
- **API tokens for bots:** a bot gets a token with explicit scopes such as `chat:write`, `chat:read:<channel>`, `tasks:read`, `tasks:write`.
  A bot only sees channels it was added to, through the same `channel_members` checks people go through.
- **Install and approval:** an "app" is a bot user plus scopes plus optional webhooks, installed by an admin, listed under Admin, Integrations, and revocable in one click.

### 3.5 Slash commands

A small registry in `internal/chat`: a message starting with `/` is matched to a command handler before it is stored.
Built-in commands (`/github`, `/task`, `/remind`, `/summarize`) are Go handlers.
External apps can register commands that TeamUp forwards as a signed HTTP request, like Slack's model.
The composer shows a command list as the person types `/`, on phone and desktop alike.

### 3.6 Which other integrations matter most

Ordered by value for a small or mid-sized Indian software organization that already runs TeamUp.

| Integration | Why | How | Notes |
| --- | --- | --- | --- |
| **Incoming webhooks** (Sentry, Grafana, CI, scripts) | Covers most alerting with no per-tool code | Section 3.3 | Do first |
| **Google Calendar and Outlook** | People already live in these calendars; TeamUp's availability and focus time are only accurate if it sees outside meetings | OAuth per user; Google push channels; Microsoft Graph change notifications | Graph event subscriptions last at most 10,080 minutes (under 7 days) and must be renewed ([Graph subscription](https://learn.microsoft.com/en-us/graph/api/resources/subscription?view=graph-rest-1.0)); Google channels also expire. Fix OIDC first so the same Google or Microsoft app serves sign-in and calendar. |
| **Jira or Linear** | Many teams keep their tracker; link and preview instead of forcing a move | Linear: webhooks and GraphQL with an API key or OAuth. Jira Cloud: OAuth 2.0 (3LO) and webhooks through the REST API, not Connect, which is ending ([Connect end of support](https://www.atlassian.com/blog/development/connect-end-of-support-what-it-means-for-custom-apps-and-how-to-migrate-to-forge)) | Make TeamUp tasks optional, not mandatory |
| **Sentry** | Production errors into the owning team's channel, assign to a person, link to a task | Start with Sentry's webhook into an incoming webhook; later an internal integration with unfurls | |
| **CI outside GitHub** (Jenkins, GitLab CI, Bitbucket Pipelines) | Build status next to the PR thread | Provider events, or incoming webhooks | |
| **Google Drive / OneDrive** | Link previews with title and owner | Per-user OAuth; preview only what the viewer can open | Lower priority, since TeamUp has its own files |

---

## 4. AI: what makes it "Slack with AI"

### 4.1 Architecture

**A shared `internal/ai` package.**
Move the `Writer` interface and the four provider clients out of `internal/notetaker` into `internal/ai`, then extend it with tool use (function calling), streaming for the assistant, structured JSON output, and an embeddings interface for later.
Admins keep choosing Claude, OpenAI, NVIDIA or a self-hosted OpenAI-compatible model, but per feature class: a fast cheap model for summaries and extraction, a stronger model for the assistant and PR summaries.

**Retrieval that runs as the asking person.**
Every AI read goes through the same service methods a page would call for that user, with that user's id:
chat search filtered by `channel_members`, tasks by the tasks rules, minutes by `notetaker.mayRead`, code by the repositories the org connected and the person's linked account.
Filtering happens before anything reaches the prompt.
The model is never trusted to withhold something it was given.
Being an admin does not widen what the assistant reads; an admin asking about a DM they are not in gets nothing from it.

**Start with search tools, not a vector database.**
Postgres full text search with trigram fallback already exists and is permission-scoped.
Give the model tools (`search_messages`, `search_tasks`, `get_thread`, `get_minutes`, `search_prs`, `my_calendar`) and let it search, read and cite.
This needs no new infrastructure and answers most "where did we decide X" questions.
Add `pgvector` hybrid search later, when people ask questions that keyword search misses.
Note that the compose files use `postgres:16-alpine`, which does not include `pgvector`, so that step means switching to an image that does (such as `pgvector/pgvector:pg16`) or installing the extension; and Anthropic does not offer an embeddings model of its own, so embeddings would come from OpenAI, NVIDIA or a self-hosted model.

**Untrusted content and actions.**
Messages, PR descriptions and minutes are input written by people and outside systems, so they can contain instructions aimed at the model.
The assistant may propose actions (create a task, post a message, comment on a PR) but a person confirms each one, and tools act with the asking person's own permissions.

**Every answer cites its sources** as links to the message, task, minutes or PR, so people can check it.

### 4.2 Features, by value

| Feature | What the person gets | Built on | Effort |
| --- | --- | --- | --- |
| **Minutes action items to tasks** | Under the minutes, each action item has "Create task", with owner matched to an attendee and the due date filled | `notetaker.ActionItem`, `tasks` create | Small, about a week |
| **Catch-up** | "What did I miss" on a channel since `last_read_at`; "Summarize" on a long thread | `channel_members.last_read_at`, chat history | Small |
| **Message to task** | From any message's menu, the model drafts title, assignee and due date from the message and its thread; the person confirms | Chat, tasks | Small |
| **Daily briefing** | At 9:00 in the person's time zone: meetings today, tasks due or overdue, PRs waiting for their review, unread mentions, decisions from yesterday's minutes; delivered as a push notification and an assistant DM | Calendar, tasks, scm events, notifications | Medium |
| **Standup draft** | Before the team's standing meeting or scrum: "Yesterday: merged PR #87 (TU-142), closed TU-150. Today: TU-151. Blocked: waiting on review of #90." The person edits and posts it | `scm_events`, task activity, calendar, `internal/tasks/scrum.go` | Medium |
| **PR summary** | In each PR thread: what changed, risk areas, linked task, CI state | scm provider, diff stats | Small after GitHub |
| **Ask your workspace** | "What did we decide about the pricing page?" with cited answers from messages, tasks, minutes and PRs the person can see | Search tools in 4.1 | Medium |
| **Assistant DM** | A bot user, "TeamUp Assistant", that a person talks to like a colleague, on desktop or phone | Bot users, `internal/ai` | Medium |
| **Voice to task** | Record a voice message to the assistant ("remind me to send the invoice to Ravi on Friday"), Sarvam transcribes, the assistant proposes a task | Voice messages, Sarvam, tasks | Small after the assistant |

**Phone.**
TeamUp is a PWA and voice messages already work, so the assistant as a DM works on a phone with no new client.
The briefing arrives as a web push notification.
Native apps are not needed for this.

### 4.3 Cost

Most of these features are short prompts over text TeamUp already has.
Rough sizes:

- Catch-up on 200 chat messages: about 8,000 to 15,000 input tokens and 500 output tokens.
- A daily briefing: about 3,000 to 6,000 input tokens per person per day.
- A meeting's minutes: up to the 120,000-character transcript cap already in `minutes.go`, roughly 30,000 tokens.
- An assistant question with tool calls: 5,000 to 30,000 tokens across the calls.

Multiply by the chosen provider's current price list; for a team of 30 using a small model for summaries and briefings, the monthly bill is likely to be modest, and the large items are long meetings and heavy assistant use.

Controls to build in from the start:

- A `ai_usage` table recording feature, user, model, input and output tokens, and estimated cost.
- An org monthly budget and a per-person daily cap, with a clear message when reached.
- Caching: a channel summary for the same message range is computed once and reused.
- A small model by default for summaries, extraction and briefings.
- The self-hosted OpenAI-compatible option for organizations that want no per-token bill and no data leaving their network.

### 4.4 Privacy

- **Where data goes.**
  With Claude, OpenAI or NVIDIA, the text in a prompt leaves the server.
  Admin settings should say so in plain words, per feature, with a switch per feature.
  Point admins at each provider's data-retention and no-training terms and commercial agreements.
- **Self-hosted model** as the fully private option, which TeamUp already supports for minutes.
- **No AI index of DMs for anyone except the DM's members.**
- **No hidden AI.**
  Anything written by AI is labelled as such, and the person who triggered it is recorded.
- **Retention.**
  Prompts and answers are not stored beyond the usage record, unless the person saves the answer.

---

## 5. Organization management and "monitoring"

### 5.1 What a manager can legitimately see

The goal "monitor them closely, what they are doing, how they are doing" is best met by watching **work outcomes and team health**, not people's screens.
TeamUp already holds most of what a manager needs:

| Signal | Source today or planned | Default level |
| --- | --- | --- |
| Task delivery: completed, open, overdue, cycle time from created to done | `internal/tasks` dashboard and contributions | Person, team, org (existing scoping) |
| Meeting attendance at standing meetings | `internal/calls/standing.go` `Attendance` | Person, team |
| Code delivery: PRs opened and merged, review turnaround, PRs waiting for review, CI failure rate | `scm_events` (planned) | Team first; person visible to the person and their manager |
| Workload: open tasks per person, overdue count, meeting hours per week | Tasks, calendar | Person and team |
| Overload signals: meeting hours above a threshold, many overdue tasks, frequent after-hours work | Calendar, tasks, scm timestamps | Shown to the person, team aggregates to managers |
| Blockers: tasks blocked or waiting on review for days | Tasks, scm | Team |
| Standup and scrum notes | `internal/tasks/scrum.go` | Team |

Present this as a **Team Health and Delivery** dashboard, built on `AnswerableFor` so the existing rule decides who answers for whom: a person sees themselves, a team lead their team, an org admin the org.

### 5.2 What not to build, and why

Do not build keystroke logging, screenshots, webcam checks, mouse-activity "active time", reports of time shown online from presence, reading other people's DMs, or ranking people by commit count or lines of code.

- **It backfires.**
  APA's Work in America surveys found monitored workers far more likely to feel stressed (56 percent against 40 percent in 2023) and micromanaged (47 percent against 23 percent in 2024); among those with low psychological safety, half said monitoring makes them want to look for another job ([APA 2023](https://www.apa.org/topics/healthy-workplaces/employee-electronic-monitoring), [APA 2024 report](https://www.apa.org/pubs/reports/work-in-america/2024/2024-work-in-america-report.pdf)).
  A review of the research links electronic monitoring to higher stress and lower job satisfaction ([Workplace Surveillance and Worker Well-Being](https://pmc.ncbi.nlm.nih.gov/articles/PMC11300163/)).
- **It measures the wrong thing.**
  Commit counts and online time are easy to game and say little about value; once they become targets people optimise the number, not the work.
- **It carries legal risk** (next section), and it makes TeamUp harder to sell to the employees who have to use it every day.

Presence dots are for "can I reach you now", never for attendance reports, and that should be written down as a product rule.

### 5.3 The law, briefly

This is a summary for product design, not legal advice.

**India: Digital Personal Data Protection Act 2023 and Rules 2025.**

- The Rules were notified on 13 November 2025, with most obligations phased in over 18 months, to about May 2027; MeitY has consulted on shortening this to 12 months, so the date may come sooner ([Seclore guide](https://www.seclore.com/fundamentals/dpdp-rules-2025-compliance-guide/), [Moneycontrol report](https://www.moneycontrol.com/news/business/meity-plans-to-cut-dpdp-compliance-window-to-12-months-for-big-tech-banks-report-13787234.html)).
- Section 7 lets an employer process personal data for employment purposes without consent, but the processing must stay tied to that purpose and be limited to what it needs.
- Obligations that TeamUp's design should help an employer meet: reasonable security safeguards, breach notification to the Data Protection Board and to affected people, and erasure when the purpose is over.
  Penalties reach 250 crore rupees for failing to take reasonable security safeguards.
- The customer organization is the data fiduciary; TeamUp is the tool, which is a selling point for self-hosting.

**EU and GDPR** (for any customer with EU staff):

- Employee monitoring must be necessary, proportionate and transparent, and usually needs a data protection impact assessment ([Article 29 Working Party Opinion 2/2017 on data processing at work](https://ec.europa.eu/newsroom/article29/items/610169)).
- The EU AI Act already bans AI that infers emotions in the workplace (since 2 February 2025), so no sentiment-from-voice features in meetings.
  AI used to monitor or evaluate workers' performance is "high-risk"; the Digital Omnibus (Regulation (EU) 2026/1744) moved those obligations to 2 December 2027, but did not remove them ([Ogletree](https://ogletree.com/insights-resources/blog-posts/eu-nears-approval-of-agreement-to-delay-rules-for-ai-use-in-employment-decisions/), [AI Act summary](https://artificialintelligenceact.eu/high-level-summary/)).
  So TeamUp's AI should summarize and assist, and should not score or rank individual employees.

### 5.4 Transparency features that make this work

- **"What my manager sees"**: every person can open the exact dashboard view their manager sees about them.
- **View log**: when a manager opens an individual's detail view, it is written to `audit_logs`, and the person can see who looked and when.
- **Notice**: an admin-editable page explaining what TeamUp records, why, who sees it, and how long it is kept, linked from Settings.
- **Retention settings** per data type (scm events, attendance, AI usage), with deletion jobs on the queue.
- **Self-first signals**: overload and after-hours signals go to the person first, and to managers only as team aggregates.
- **Export**: a person can download their own data, which also helps with DPDP and GDPR access requests.

---

## 6. Phased roadmap

Efforts are rough, in engineer-weeks for one experienced developer on this codebase, including tests in the project's style (real Postgres, a second org in every org-scoped test) and end-to-end tests.

### Phase 0: Foundations (2 to 3 weeks)

- Encrypted secret store (HKDF from `TEAMUP_AUTH_SECRET`, AES-256-GCM), with migration of note-taker keys and `linked_identities` tokens.
- Postgres job queue, multi-replica safe.
- Task keys: org prefix and sequence, shown everywhere a task is shown, searchable.
- Bot users (`users.kind`), integration messages (`messages.kind = 'integration'` with cards), and the card renderer in `web/src/features/chat`.
- Incoming webhooks per channel, Slack-compatible body.
- `audit_logs` writes for admin and integration actions.
- New package `internal/integrations`, new tab `web/src/features/admin/IntegrationsTab.tsx`.

Value on its own: Sentry, Grafana and CI alerts in channels on day one.

### Phase 1: GitHub core (4 to 6 weeks)

- `internal/integrations/scm` with the GitHub provider.
- Manifest-flow registration, installation, repository selection, base URL setting for GHES.
- Webhook receiver with signature check, dedupe, queueing, and failed-delivery redelivery.
- Member linking in Settings, identity resolver, admin confirmation of suggested matches.
- Channel subscriptions, one thread per PR, personal notifications for reviews and CI.
- Unfurls for PR, issue, commit and code-line links.
- Task links from keys in branch, title and commit messages; Code panel on the task page; optional "Fixes" auto-close.
- Slash commands `/github` and `/task`.

In parallel, about one week: **minutes action items to tasks** in `internal/notetaker` and the Meeting notes page.

### Phase 2: AI basics (3 to 4 weeks)

- `internal/ai` lifted from `internal/notetaker`, with per-feature model choice, usage table, budgets and caching.
- Thread and channel catch-up, message to task, PR summary.
- Admin AI settings page extended from `NotetakerTab.tsx`, with per-feature switches and plain-words data notice.

### Phase 3: Personal assistant and delivery dashboards (4 to 6 weeks)

- Assistant bot DM with search tools, cited answers, confirmed actions, voice input.
- Daily briefing (scheduled job, push notification).
- Standup draft into scrums and standing meetings.
- Team Health and Delivery dashboard: code metrics from `scm_events` beside the existing task and attendance data, using `AnswerableFor`.
- "What my manager sees", view log, notice page, retention settings.

### Phase 4: More providers and calendars (5 to 8 weeks)

- Bitbucket Cloud and GitLab providers behind the same interface.
- Make OIDC real (issue #15, #31), then Google Calendar and Outlook sync with subscription renewal jobs.
- Two-way PR comments from TeamUp threads.

### Phase 5: Platform (open-ended)

- `pgvector` hybrid search for the assistant.
- Jira and Linear linking and previews.
- Outgoing webhooks, scoped bot API tokens, external slash commands: a public app model.
- Drive and OneDrive previews.

### What to build first

Phase 0 plus Phase 1, with minutes-to-tasks alongside.
The owner asked for GitHub by name, it serves the developer teams most likely to adopt TeamUp, and the foundations it forces (secrets, jobs, bots, task keys) are what every later integration and every AI feature needs.
Catch-up and the daily briefing come right after, because they are what makes people open TeamUp on their phone every morning.

---

## Decisions the owner needs to make

1. **Webhook reachability.**
   Require a public HTTPS address for code integrations, or also support LAN-only servers through polling or a relay?
2. **Task key format.**
   One org-wide prefix (`TU-142`) now, or per-project keys (`API-12`, `WEB-7`), which would pull the `projects` table back into scope?
3. **Private code in chat.**
   May previews of private-repo code and PRs appear in any channel, only in private channels, or only for people with a linked account that can see the repo?
4. **Two-way GitHub.**
   Read-only first (fewer permissions, simpler), or allow posting PR comments and approvals from TeamUp in the first release?
5. **AI data boundary.**
   Default provider for new installs, and whether message content may go to a cloud model at all, or only to a self-hosted one; plus default monthly budget.
6. **Individual code metrics.**
   Should managers see per-person code activity, or only team-level by default with per-person visible to the person?
7. **Monitoring policy.**
   Adopt "no keystroke, screenshot or online-time tracking" as a written product rule, and ship the transparency features with the dashboards rather than after?
8. **Bitbucket timing.**
   Is there a current customer on Bitbucket Cloud, or can it wait for Phase 4?
9. **Vector search.**
   Accept a Postgres image change for `pgvector` later, or commit to keyword search plus model tools only?
10. **Secrets migration.**
    Agree that existing plain-text secrets are encrypted in Phase 0, including on the live server, with a tested rollback.

---

## Sources

GitHub:

- [Differences between GitHub Apps and OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps)
- [Registering a GitHub App from a manifest](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest)
- [Registering a GitHub App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app)
- [Making your GitHub App available for GitHub Enterprise Server](https://docs.github.com/github-ae@latest/apps/sharing-github-apps/making-your-github-app-available-for-github-enterprise-server)
- [Sharing GitHub Apps, GHES 3.22](https://docs.github.com/en/enterprise-server@3.22/apps/sharing-github-apps)
- [Rate limits for the REST API](https://docs.github.com/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- [Rate limits for the GraphQL API](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api)
- [Refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens)
- [Authenticating as a GitHub App installation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation)
- [Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)
- [Best practices for using webhooks](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)
- [Handling failed webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries)

Bitbucket, Atlassian and GitLab:

- [Bitbucket Cloud transitions to API tokens](https://www.atlassian.com/blog/bitbucket/bitbucket-cloud-transitions-to-api-tokens-enhancing-security-with-app-password-deprecation)
- [Bitbucket Cloud Connect end of support timeline](https://community.developer.atlassian.com/t/bitbucket-cloud-announcing-connect-end-of-support-timeline-and-next-steps/98760)
- [Bitbucket Cloud Connect end of support: migration responsibilities](https://community.developer.atlassian.com/t/bitbucket-cloud-connect-end-of-support-understanding-app-migration-responsibilities/103316)
- [Connect end of support and migrating to Forge](https://www.atlassian.com/blog/development/connect-end-of-support-what-it-means-for-custom-apps-and-how-to-migrate-to-forge)
- [Use OAuth on Bitbucket Cloud](https://support.atlassian.com/bitbucket-cloud/docs/use-oauth-on-bitbucket-cloud/)
- [Bitbucket workspace access tokens](https://support.atlassian.com/bitbucket-cloud/docs/using-workspace-access-tokens/)
- [Bitbucket Workspaces API (webhooks)](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-workspaces/)
- [Bitbucket manage webhooks](https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/)
- [GitLab group access tokens API](https://docs.gitlab.com/api/group_access_tokens/)
- [GitLab REST API authentication](https://docs.gitlab.com/api/rest/authentication/)
- [GitLab OAuth 2.0 provider API](https://docs.gitlab.com/api/oauth2/)

Other platforms:

- [Microsoft Graph subscription lifetimes](https://learn.microsoft.com/en-us/graph/api/resources/subscription?view=graph-rest-1.0)
- [Slack rate limit changes for non-Marketplace apps (2025)](https://docs.slack.dev/changelog/2025/05/29/rate-limit-changes-for-non-marketplace-apps), which shows Slack limiting third-party access to message history to 1 request per minute and 15 messages for new non-Marketplace apps, citing AI data scraping; owning the data is a real advantage of a self-hosted workspace.
- [River, a Postgres job queue for Go](https://riverqueue.com/)

Law and research:

- [DPDP Rules 2025 compliance guide (Seclore)](https://www.seclore.com/fundamentals/dpdp-rules-2025-compliance-guide/)
- [MeitY plans to cut DPDP compliance window (Moneycontrol)](https://www.moneycontrol.com/news/business/meity-plans-to-cut-dpdp-compliance-window-to-12-months-for-big-tech-banks-report-13787234.html)
- [Article 29 Working Party Opinion 2/2017 on data processing at work](https://ec.europa.eu/newsroom/article29/items/610169)
- [EU AI Act high-level summary](https://artificialintelligenceact.eu/high-level-summary/)
- [EU delays AI Act rules for employment decisions (Ogletree)](https://ogletree.com/insights-resources/blog-posts/eu-nears-approval-of-agreement-to-delay-rules-for-ai-use-in-employment-decisions/)
- [APA: electronic monitoring and mental health (2023)](https://www.apa.org/topics/healthy-workplaces/employee-electronic-monitoring)
- [APA 2024 Work in America report](https://www.apa.org/pubs/reports/work-in-america/2024/2024-work-in-america-report.pdf)
- [Workplace Surveillance and Worker Well-Being (PMC)](https://pmc.ncbi.nlm.nih.gov/articles/PMC11300163/)
