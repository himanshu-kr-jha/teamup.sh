package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// ── keys ──────────────────────────────────────────────────────────────────

// taskKey finds keys such as PHO-12 in a title, a branch (pho-12-fix-login)
// or a commit message, in any case.
var taskKey = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])([A-Za-z][A-Za-z0-9]{1,9}-[0-9]{1,7})(?:$|[^0-9])`)

// KeysIn lists the task keys named in the texts, upper-cased, each once.
func KeysIn(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range taskKey.FindAllStringSubmatch(t, -1) {
			k := strings.ToUpper(m[1])
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// ── receiving ─────────────────────────────────────────────────────────────

// ErrBadSignature is an event that did not come from the app.
var ErrBadSignature = apperrors.New(apperrors.CodeUnauthorized, "signature does not match")

// GitHubEvent takes one event GitHub sent to a connection's address. It checks
// the signature against the app's webhook secret, acts on each delivery once,
// and returns quickly: GitHub gives up on a slow answer.
func (s *Service) GitHubEvent(ctx context.Context, hookKey, event, delivery, signature string, body []byte) error {
	c, err := s.loadConnection(ctx, `hook_key = $1 AND provider = 'github'`, hookKey)
	if err != nil {
		return err
	}
	sec, err := s.githubSecrets(c)
	if err != nil {
		return err
	}
	if !validSignature(sec.WebhookSecret, signature, body) {
		return ErrBadSignature
	}
	if delivery != "" {
		tag, err := s.db.Exec(ctx, `INSERT INTO code_deliveries (connection_id, delivery_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, c.ID, delivery)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil // seen already
		}
	}
	err = s.dispatch(ctx, c, event, body)
	if err != nil && delivery != "" {
		// Failed partway: GitHub retries it, and the retry must be acted on.
		_, _ = s.db.Exec(ctx, `DELETE FROM code_deliveries WHERE connection_id = $1 AND delivery_id = $2`, c.ID, delivery)
	}
	return err
}

func (s *Service) dispatch(ctx context.Context, c *connection, event string, body []byte) error {
	switch event {
	case "installation":
		return s.onInstallation(ctx, c, body)
	case "installation_repositories":
		return s.onInstallationRepositories(ctx, c, body)
	case "pull_request":
		return s.onPullRequest(ctx, c, body)
	case "pull_request_review":
		return s.onReview(ctx, c, body)
	case "push":
		return s.onPush(ctx, c, body)
	case "check_suite":
		return s.onCheckSuite(ctx, c, body)
	case "issue_comment":
		return s.onComment(ctx, c, body)
	case "member":
		return s.onMember(ctx, c, body)
	}
	return nil
}

func validSignature(secret, header string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// ── the installation ──────────────────────────────────────────────────────

func (s *Service) onInstallation(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64  `json:"id"`
			Account ghUser `json:"account"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	switch e.Action {
	case "created", "unsuspend", "new_permissions_accepted":
		_, err := s.db.Exec(ctx, `UPDATE code_connections SET installation = $2, account = $3, status = 'active', last_error = '', updated_at = NOW() WHERE id = $1`,
			c.ID, fmt.Sprint(e.Installation.ID), e.Installation.Account.Login)
		if err != nil {
			return err
		}
		c.Installation = fmt.Sprint(e.Installation.ID)
		return s.syncRepositories(ctx, c)
	case "deleted", "suspend":
		s.gh.forget(c.Installation)
		_, err := s.db.Exec(ctx, `UPDATE code_connections SET status = 'broken', last_error = $2, updated_at = NOW() WHERE id = $1`,
			c.ID, "The GitHub App was "+map[string]string{"deleted": "uninstalled", "suspend": "suspended"}[e.Action]+" on GitHub. Install it again to carry on.")
		return err
	}
	return nil
}

func (s *Service) onInstallationRepositories(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Added   []ghRepo `json:"repositories_added"`
		Removed []ghRepo `json:"repositories_removed"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	for _, r := range e.Added {
		if err := upsertRepo(ctx, s.db, c, r.info()); err != nil {
			return err
		}
	}
	for _, r := range e.Removed {
		if _, err := s.db.Exec(ctx, `UPDATE code_repositories SET present = FALSE WHERE connection_id = $1 AND external_id = $2`, c.ID, fmt.Sprint(r.ID)); err != nil {
			return err
		}
	}
	return nil
}

// repoFor finds, or records, the repository an event is about.
func (s *Service) repoFor(ctx context.Context, c *connection, r repoInfo) (uuid.UUID, error) {
	if err := upsertRepo(ctx, s.db, c, r); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT id FROM code_repositories WHERE connection_id = $1 AND external_id = $2`, c.ID, r.ExternalID).Scan(&id)
	return id, err
}

// personFor is the TeamUp person behind an account on a provider: its GitHub
// login, its Bitbucket id, or one of the addresses they commit with, once they
// have linked it.
func (s *Service) personFor(ctx context.Context, orgID uuid.UUID, provider, key, email string) *uuid.UUID {
	if key == "" && email == "" {
		return nil
	}
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT user_id FROM code_identities
		WHERE org_id = $1 AND provider = $2
		  AND (($3 <> '' AND (LOWER(login) = LOWER($3) OR external_id = $3 OR account_id = $3))
		       OR ($4 <> '' AND LOWER($4) = ANY(SELECT LOWER(e) FROM UNNEST(emails) e)))
		LIMIT 1
	`, orgID, provider, key, email).Scan(&id)
	if err != nil {
		return nil
	}
	return &id
}

// ── pull requests ─────────────────────────────────────────────────────────

func (s *Service) onPullRequest(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Action            string `json:"action"`
		PullRequest       ghPull `json:"pull_request"`
		Repository        ghRepo `json:"repository"`
		Sender            ghUser `json:"sender"`
		RequestedReviewer ghUser `json:"requested_reviewer"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	pr, err := s.storePull(ctx, c, e.Repository.info(), e.PullRequest.info())
	if err != nil {
		return err
	}
	switch e.Action {
	case "review_requested":
		if e.RequestedReviewer.Login != "" {
			if err := s.setReviewer(ctx, c.OrgID, pr, e.RequestedReviewer.Login, e.RequestedReviewer.Login, "requested"); err != nil {
				return err
			}
			s.tellReviewer(ctx, c.OrgID, pr, e.RequestedReviewer.Login)
		}
		s.moveLinked(ctx, c.OrgID, pr, "in_review", fmt.Sprintf("#%d in %s is waiting for review", pr.number, pr.repoName))
	case "review_request_removed":
		if e.RequestedReviewer.Login != "" {
			_, _ = s.db.Exec(ctx, `DELETE FROM code_pull_request_reviewers WHERE pull_request_id = $1 AND LOWER(login) = LOWER($2) AND state = 'requested'`, pr.id, e.RequestedReviewer.Login)
		}
	case "opened", "reopened", "ready_for_review":
		for _, r := range e.PullRequest.RequestedReviewers {
			if err := s.setReviewer(ctx, c.OrgID, pr, r.Login, r.Login, "requested"); err != nil {
				return err
			}
			if e.Action != "reopened" {
				s.tellReviewer(ctx, c.OrgID, pr, r.Login)
			}
		}
		to := "in_progress"
		if len(e.PullRequest.RequestedReviewers) > 0 && pr.state == "open" {
			to = "in_review"
		}
		s.moveLinked(ctx, c.OrgID, pr, to, fmt.Sprintf("%s opened #%d in %s", e.PullRequest.User.Login, pr.number, pr.repoName))
	case "closed":
		if pr.state == "merged" {
			s.onMerged(ctx, c.OrgID, pr, e.Sender.Login)
		}
	}
	return nil
}

type storedPull struct {
	provider string
	id       uuid.UUID
	number   int
	title    string
	url      string
	state    string
	repoName string
	author   string
	authorID *uuid.UUID
	tasks    []uuid.UUID
}

// pullInfo is a pull request as any provider describes it.
type pullInfo struct {
	Number      int
	Title       string
	Body        string
	State       string // open, draft, merged or closed
	URL         string
	Head, Base  string
	AuthorLogin string // shown
	AuthorKey   string // matched to a linked account: a GitHub login, a Bitbucket uuid
	OpenedAt    time.Time
	MergedAt    *time.Time
	ClosedAt    *time.Time
}

func (p ghPull) info() pullInfo {
	return pullInfo{Number: p.Number, Title: p.Title, Body: p.Body, State: p.state(), URL: p.HTMLURL,
		Head: p.Head.Ref, Base: p.Base.Ref, AuthorLogin: p.User.Login, AuthorKey: p.User.Login,
		OpenedAt: p.CreatedAt, MergedAt: p.MergedAt, ClosedAt: p.ClosedAt}
}

// storePull keeps the pull request as the provider has it now, and links it to
// every task its title, branch or description names.
func (s *Service) storePull(ctx context.Context, c *connection, repo repoInfo, p pullInfo) (*storedPull, error) {
	repoID, err := s.repoFor(ctx, c, repo)
	if err != nil {
		return nil, err
	}
	pr := &storedPull{provider: c.Provider, number: p.Number, title: p.Title, url: p.URL, state: p.State, repoName: repo.FullName,
		author: p.AuthorLogin, authorID: s.personFor(ctx, c.OrgID, c.Provider, p.AuthorKey, "")}
	opened := p.OpenedAt
	if opened.IsZero() {
		opened = time.Now()
	}
	if err := s.db.QueryRow(ctx, `
		INSERT INTO code_pull_requests (org_id, repository_id, number, title, state, html_url, head_branch, base_branch,
		                                author_login, author_id, opened_at, merged_at, closed_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
		ON CONFLICT (repository_id, number) DO UPDATE SET title = EXCLUDED.title, state = EXCLUDED.state,
		    html_url = EXCLUDED.html_url, head_branch = EXCLUDED.head_branch, base_branch = EXCLUDED.base_branch,
		    author_login = EXCLUDED.author_login, author_id = COALESCE(EXCLUDED.author_id, code_pull_requests.author_id),
		    merged_at = EXCLUDED.merged_at, closed_at = EXCLUDED.closed_at, updated_at = NOW()
		RETURNING id
	`, c.OrgID, repoID, p.Number, p.Title, pr.state, p.URL, p.Head, p.Base, p.AuthorLogin, pr.authorID,
		opened, p.MergedAt, p.ClosedAt).Scan(&pr.id); err != nil {
		return nil, fmt.Errorf("store pull request: %w", err)
	}
	for _, key := range KeysIn(p.Title, p.Head, p.Body) {
		task, err := s.tasks.TaskByKey(ctx, c.OrgID, key)
		if err != nil {
			continue
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO task_code_links (task_id, pull_request_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, task.ID, pr.id); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.Query(ctx, `SELECT task_id FROM task_code_links WHERE pull_request_id = $1`, pr.id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			pr.tasks = append(pr.tasks, id)
		}
	}
	rows.Close()
	return pr, nil
}

// setReviewer records what a reviewer, shown as login and known by key, said.
func (s *Service) setReviewer(ctx context.Context, orgID uuid.UUID, pr *storedPull, login, key, state string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO code_pull_request_reviewers (pull_request_id, login, user_id, state, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (pull_request_id, login) DO UPDATE SET state = EXCLUDED.state,
		    user_id = COALESCE(EXCLUDED.user_id, code_pull_request_reviewers.user_id), updated_at = NOW()
	`, pr.id, login, s.personFor(ctx, orgID, pr.provider, key, ""), state)
	return err
}

// tell notifies somebody, best effort. A pull request linked to a task opens
// the task; anything else opens the pull request on GitHub.
func (s *Service) tell(ctx context.Context, orgID uuid.UUID, who *uuid.UUID, kind, title, body string, pr *storedPull) {
	if s.notifier == nil || who == nil {
		return
	}
	data := map[string]any{"url": pr.url}
	if len(pr.tasks) > 0 {
		data["taskId"] = pr.tasks[0].String()
	}
	if err := s.notifier.Notify(ctx, orgID, *who, kind, title, body, data); err != nil {
		s.logger.Warn().Err(err).Msg("code notification failed")
	}
}

func (s *Service) tellReviewer(ctx context.Context, orgID uuid.UUID, pr *storedPull, key string) {
	s.tell(ctx, orgID, s.personFor(ctx, orgID, pr.provider, key, ""), "code.review_requested",
		"Your review is wanted: "+pr.title,
		fmt.Sprintf("%s asked you to review #%d in %s", pr.author, pr.number, pr.repoName), pr)
}

// moveLinked moves every task the pull request names forward, if it can.
func (s *Service) moveLinked(ctx context.Context, orgID uuid.UUID, pr *storedPull, to, why string) {
	for _, taskID := range pr.tasks {
		if _, err := s.tasks.MoveByCode(ctx, orgID, taskID, to, pr.provider, pr.authorID, why); err != nil {
			s.logger.Warn().Err(err).Msg("moving a task for its pull request failed")
		}
	}
}

// onMerged finishes a task once every pull request linked to it is merged:
// the frontend half alone being in does not make the work done.
func (s *Service) onMerged(ctx context.Context, orgID uuid.UUID, pr *storedPull, mergedBy string) {
	for _, taskID := range pr.tasks {
		var open int
		if err := s.db.QueryRow(ctx, `
			SELECT count(*) FROM task_code_links l JOIN code_pull_requests p ON p.id = l.pull_request_id
			WHERE l.task_id = $1 AND p.state IN ('open', 'draft')
		`, taskID).Scan(&open); err != nil || open > 0 {
			continue
		}
		if _, err := s.tasks.MoveByCode(ctx, orgID, taskID, "done", pr.provider, pr.authorID,
			fmt.Sprintf("%s merged #%d in %s", mergedBy, pr.number, pr.repoName)); err != nil {
			s.logger.Warn().Err(err).Msg("finishing a task for its pull request failed")
		}
	}
}

func (s *Service) onReview(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Action string `json:"action"`
		Review struct {
			State string `json:"state"`
			User  ghUser `json:"user"`
		} `json:"review"`
		PullRequest ghPull `json:"pull_request"`
		Repository  ghRepo `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	if e.Action != "submitted" {
		return nil
	}
	pr, err := s.storePull(ctx, c, e.Repository.info(), e.PullRequest.info())
	if err != nil {
		return err
	}
	state := strings.ToLower(e.Review.State)
	if err := s.setReviewer(ctx, c.OrgID, pr, e.Review.User.Login, e.Review.User.Login, state); err != nil {
		return err
	}
	if strings.EqualFold(e.Review.User.Login, pr.author) {
		return nil
	}
	headline := map[string]string{
		"approved":          "approved",
		"changes_requested": "asked for changes on",
		"commented":         "reviewed",
	}[state]
	if headline == "" {
		return nil
	}
	s.tell(ctx, c.OrgID, pr.authorID, "code.review", fmt.Sprintf("%s %s your pull request", e.Review.User.Login, headline),
		fmt.Sprintf("#%d %s in %s", pr.number, pr.title, pr.repoName), pr)
	return nil
}

// onCheckSuite records whether the checks passed, and tells the author when they did not.
func (s *Service) onCheckSuite(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Action     string `json:"action"`
		CheckSuite struct {
			Conclusion   string `json:"conclusion"`
			PullRequests []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
		} `json:"check_suite"`
		Repository ghRepo `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	if e.Action != "completed" {
		return nil
	}
	result := map[string]string{"success": "passing", "neutral": "passing", "skipped": "passing", "failure": "failing", "timed_out": "failing", "action_required": "failing", "cancelled": "failing"}[e.CheckSuite.Conclusion]
	if result == "" {
		return nil
	}
	repoID, err := s.repoFor(ctx, c, e.Repository.info())
	if err != nil {
		return err
	}
	for _, p := range e.CheckSuite.PullRequests {
		pr := storedPull{provider: c.Provider}
		var before string
		err := s.db.QueryRow(ctx, `
			SELECT id, number, title, html_url, author_login, author_id, checks FROM code_pull_requests
			WHERE repository_id = $1 AND number = $2
		`, repoID, p.Number).Scan(&pr.id, &pr.number, &pr.title, &pr.url, &pr.author, &pr.authorID, &before)
		if err != nil {
			continue
		}
		pr.repoName = e.Repository.FullName
		if _, err := s.db.Exec(ctx, `UPDATE code_pull_requests SET checks = $2, updated_at = NOW() WHERE id = $1`, pr.id, result); err != nil {
			return err
		}
		if result == "failing" && before != "failing" {
			s.tell(ctx, c.OrgID, pr.authorID, "code.checks_failed", "Checks failed on your pull request",
				fmt.Sprintf("#%d %s in %s", pr.number, pr.title, pr.repoName), &pr)
		}
	}
	return nil
}

// onComment tells anybody named with @ in a pull request's conversation.
func (s *Service) onComment(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Action string `json:"action"`
		Issue  struct {
			Number      int       `json:"number"`
			Title       string    `json:"title"`
			HTMLURL     string    `json:"html_url"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			Body    string `json:"body"`
			HTMLURL string `json:"html_url"`
			User    ghUser `json:"user"`
		} `json:"comment"`
		Repository ghRepo `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	if e.Action != "created" || e.Issue.PullRequest == nil {
		return nil
	}
	pr := &storedPull{provider: c.Provider, number: e.Issue.Number, title: e.Issue.Title, url: e.Comment.HTMLURL, repoName: e.Repository.FullName}
	if repoID, err := s.repoFor(ctx, c, e.Repository.info()); err == nil {
		rows, err := s.db.Query(ctx, `
			SELECT l.task_id FROM task_code_links l JOIN code_pull_requests p ON p.id = l.pull_request_id
			WHERE p.repository_id = $1 AND p.number = $2`, repoID, e.Issue.Number)
		if err == nil {
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) == nil {
					pr.tasks = append(pr.tasks, id)
				}
			}
			rows.Close()
		}
	}
	told := map[string]bool{strings.ToLower(e.Comment.User.Login): true}
	for _, m := range githubMention.FindAllStringSubmatch(e.Comment.Body, -1) {
		login := strings.ToLower(m[1])
		if told[login] {
			continue
		}
		told[login] = true
		s.tell(ctx, c.OrgID, s.personFor(ctx, c.OrgID, c.Provider, login, ""), "code.mention",
			fmt.Sprintf("%s mentioned you on #%d", e.Comment.User.Login, e.Issue.Number),
			fmt.Sprintf("%s in %s", e.Issue.Title, e.Repository.FullName), pr)
	}
	return nil
}

var githubMention = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])@([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))`)

// ── commits ───────────────────────────────────────────────────────────────

func (s *Service) onPush(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Ref        string `json:"ref"`
		Deleted    bool   `json:"deleted"`
		Repository ghRepo `json:"repository"`
		Commits    []struct {
			ID        string    `json:"id"`
			Message   string    `json:"message"`
			URL       string    `json:"url"`
			Timestamp time.Time `json:"timestamp"`
			Distinct  bool      `json:"distinct"`
			Author    struct {
				Name     string `json:"name"`
				Email    string `json:"email"`
				Username string `json:"username"`
			} `json:"author"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	if e.Deleted || !strings.HasPrefix(e.Ref, "refs/heads/") {
		return nil
	}
	commits := make([]commitInfo, 0, len(e.Commits))
	for _, cm := range e.Commits {
		commits = append(commits, commitInfo{SHA: cm.ID, Message: cm.Message, URL: cm.URL, At: cm.Timestamp,
			AuthorLogin: cm.Author.Username, AuthorKey: cm.Author.Username, AuthorEmail: cm.Author.Email, AuthorName: cm.Author.Name})
	}
	return s.storeCommits(ctx, c, e.Repository.info(), strings.TrimPrefix(e.Ref, "refs/heads/"), commits)
}

// commitInfo is a commit as any provider describes it.
type commitInfo struct {
	SHA, Message, URL string
	At                time.Time
	AuthorLogin       string
	AuthorKey         string
	AuthorEmail       string
	AuthorName        string
}

// storeCommits keeps the commits pushed to a branch, links each to the tasks
// it or the branch names, and puts a task that was waiting in progress.
func (s *Service) storeCommits(ctx context.Context, c *connection, repo repoInfo, branch string, commits []commitInfo) error {
	repoID, err := s.repoFor(ctx, c, repo)
	if err != nil {
		return err
	}
	for _, cm := range commits {
		author := s.personFor(ctx, c.OrgID, c.Provider, cm.AuthorKey, cm.AuthorEmail)
		at := cm.At
		if at.IsZero() {
			at = time.Now()
		}
		var id uuid.UUID
		if err := s.db.QueryRow(ctx, `
			INSERT INTO code_commits (org_id, repository_id, sha, message, branch, html_url, author_login, author_email, author_name, author_id, committed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (repository_id, sha) DO UPDATE SET author_id = COALESCE(EXCLUDED.author_id, code_commits.author_id)
			RETURNING id
		`, c.OrgID, repoID, cm.SHA, cm.Message, branch, cm.URL, cm.AuthorLogin, cm.AuthorEmail, cm.AuthorName, author, at).Scan(&id); err != nil {
			return fmt.Errorf("store commit: %w", err)
		}
		for _, key := range KeysIn(cm.Message, branch) {
			task, err := s.tasks.TaskByKey(ctx, c.OrgID, key)
			if err != nil {
				continue
			}
			tag, err := s.db.Exec(ctx, `INSERT INTO task_code_links (task_id, commit_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, task.ID, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				_, _ = s.tasks.MoveByCode(ctx, c.OrgID, task.ID, "in_progress", c.Provider, author,
					fmt.Sprintf("%s pushed a commit to %s in %s", firstNonEmpty(cm.AuthorLogin, cm.AuthorName), branch, repo.FullName))
			}
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
