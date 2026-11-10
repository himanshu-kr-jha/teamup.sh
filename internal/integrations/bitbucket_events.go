package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BitbucketEvent takes one event Bitbucket sent to the workspace's webhook:
// signed with the webhook's secret, and acted on once.
func (s *Service) BitbucketEvent(ctx context.Context, hookKey, event, delivery, signature string, body []byte) error {
	c, err := s.loadConnection(ctx, `hook_key = $1 AND provider = 'bitbucket'`, hookKey)
	if err != nil {
		return err
	}
	sec, err := s.bitbucketSecrets(c)
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
			return nil
		}
	}
	if err := s.bitbucketDispatch(ctx, c, event, body); err != nil {
		if delivery != "" {
			_, _ = s.db.Exec(ctx, `DELETE FROM code_deliveries WHERE connection_id = $1 AND delivery_id = $2`, c.ID, delivery)
		}
		return err
	}
	return nil
}

func (s *Service) bitbucketDispatch(ctx context.Context, c *connection, event string, body []byte) error {
	switch {
	case event == "repo:push":
		return s.onBitbucketPush(ctx, c, body)
	case strings.HasPrefix(event, "repo:commit_status_"):
		return s.onBitbucketStatus(ctx, c, body)
	case event == "repo:created":
		var e struct {
			Repository bbRepo `json:"repository"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return err
		}
		return upsertRepo(ctx, s.db, c, e.Repository.info())
	case event == "repo:deleted":
		var e struct {
			Repository bbRepo `json:"repository"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return err
		}
		_, err := s.db.Exec(ctx, `UPDATE code_repositories SET present = FALSE WHERE connection_id = $1 AND external_id = $2`, c.ID, e.Repository.UUID)
		return err
	case event == "pullrequest:comment_created":
		return s.onBitbucketComment(ctx, c, body)
	case strings.HasPrefix(event, "pullrequest:"):
		return s.onBitbucketPull(ctx, c, strings.TrimPrefix(event, "pullrequest:"), body)
	}
	return nil
}

type bbPullEvent struct {
	Actor          bbAccount                 `json:"actor"`
	PullRequest    bbPull                    `json:"pullrequest"`
	Repository     bbRepo                    `json:"repository"`
	Approval       *struct{ User bbAccount } `json:"approval"`
	ChangesRequest *struct{ User bbAccount } `json:"changes_request"`
}

func (s *Service) onBitbucketPull(ctx context.Context, c *connection, action string, body []byte) error {
	var e bbPullEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	// Who was asked to review before this, by name: Bitbucket says nothing when
	// a reviewer is added, so the list is compared with the last one seen.
	before := map[string]bool{}
	repoID, err := s.repoFor(ctx, c, e.Repository.info())
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.login FROM code_pull_request_reviewers r JOIN code_pull_requests p ON p.id = r.pull_request_id
		WHERE p.repository_id = $1 AND p.number = $2`, repoID, e.PullRequest.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var login string
		if rows.Scan(&login) == nil {
			before[login] = true
		}
	}
	rows.Close()

	pr, err := s.storePull(ctx, c, e.Repository.info(), e.PullRequest.info())
	if err != nil {
		return err
	}
	now := map[string]bool{}
	var added []bbAccount
	for _, r := range e.PullRequest.Reviewers {
		now[r.name()] = true
		if !before[r.name()] {
			added = append(added, r)
			if err := s.setReviewer(ctx, c.OrgID, pr, r.name(), r.UUID, "requested"); err != nil {
				return err
			}
		}
	}
	// Somebody taken off the list, who had not said anything yet, is no longer asked.
	for login := range before {
		if !now[login] {
			_, _ = s.db.Exec(ctx, `DELETE FROM code_pull_request_reviewers WHERE pull_request_id = $1 AND login = $2 AND state = 'requested'`, pr.id, login)
		}
	}
	if pr.state == "open" {
		for _, r := range added {
			s.tellReviewer(ctx, c.OrgID, pr, r.UUID)
		}
	}

	switch action {
	case "created":
		to := "in_progress"
		if len(e.PullRequest.Reviewers) > 0 && pr.state == "open" {
			to = "in_review"
		}
		s.moveLinked(ctx, c.OrgID, pr, to, fmt.Sprintf("%s opened #%d in %s", pr.author, pr.number, pr.repoName))
	case "updated":
		if len(added) > 0 && pr.state == "open" {
			s.moveLinked(ctx, c.OrgID, pr, "in_review", fmt.Sprintf("#%d in %s is waiting for review", pr.number, pr.repoName))
		}
	case "approved", "unapproved", "changes_request_created", "changes_request_removed":
		var who bbAccount
		state := "requested"
		switch action {
		case "approved":
			state = "approved"
		case "changes_request_created":
			state = "changes_requested"
		}
		if e.Approval != nil {
			who = e.Approval.User
		} else if e.ChangesRequest != nil {
			who = e.ChangesRequest.User
		} else {
			who = e.Actor
		}
		if err := s.setReviewer(ctx, c.OrgID, pr, who.name(), who.UUID, state); err != nil {
			return err
		}
		if who.UUID == e.PullRequest.Author.UUID || (state != "approved" && state != "changes_requested") {
			return nil
		}
		verb := map[string]string{"approved": "approved", "changes_requested": "asked for changes on"}[state]
		s.tell(ctx, c.OrgID, pr.authorID, "code.review", fmt.Sprintf("%s %s your pull request", who.name(), verb),
			fmt.Sprintf("#%d %s in %s", pr.number, pr.title, pr.repoName), pr)
	case "fulfilled":
		s.onMerged(ctx, c.OrgID, pr, e.Actor.name())
	}
	return nil
}

// A Bitbucket mention is written @{account id}, whatever the person is called.
var bitbucketMention = regexp.MustCompile(`@\{([^}]+)\}`)

func (s *Service) onBitbucketComment(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Actor   bbAccount `json:"actor"`
		Comment struct {
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
			Links bbLinks `json:"links"`
		} `json:"comment"`
		PullRequest bbPull `json:"pullrequest"`
		Repository  bbRepo `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	pr := &storedPull{provider: c.Provider, number: e.PullRequest.ID, title: e.PullRequest.Title,
		url: firstNonEmpty(e.Comment.Links.HTML.Href, e.PullRequest.Links.HTML.Href), repoName: e.Repository.FullName}
	if repoID, err := s.repoFor(ctx, c, e.Repository.info()); err == nil {
		rows, err := s.db.Query(ctx, `
			SELECT l.task_id FROM task_code_links l JOIN code_pull_requests p ON p.id = l.pull_request_id
			WHERE p.repository_id = $1 AND p.number = $2`, repoID, e.PullRequest.ID)
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
	told := map[string]bool{e.Actor.AccountID: true}
	for _, m := range bitbucketMention.FindAllStringSubmatch(e.Comment.Content.Raw, -1) {
		if told[m[1]] {
			continue
		}
		told[m[1]] = true
		s.tell(ctx, c.OrgID, s.personFor(ctx, c.OrgID, c.Provider, m[1], ""), "code.mention",
			fmt.Sprintf("%s mentioned you on #%d", e.Actor.name(), e.PullRequest.ID),
			fmt.Sprintf("%s in %s", e.PullRequest.Title, e.Repository.FullName), pr)
	}
	return nil
}

// author splits Bitbucket's "Name <email>".
func splitAuthor(raw string) (name, email string) {
	if i := strings.LastIndex(raw, "<"); i >= 0 && strings.HasSuffix(raw, ">") {
		return strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1 : len(raw)-1])
	}
	return strings.TrimSpace(raw), ""
}

func (s *Service) onBitbucketPush(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Repository bbRepo `json:"repository"`
		Push       struct {
			Changes []struct {
				New *struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"new"`
				Commits []struct {
					Hash    string    `json:"hash"`
					Message string    `json:"message"`
					Date    time.Time `json:"date"`
					Links   bbLinks   `json:"links"`
					Author  struct {
						Raw  string    `json:"raw"`
						User bbAccount `json:"user"`
					} `json:"author"`
				} `json:"commits"`
			} `json:"changes"`
		} `json:"push"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	for _, ch := range e.Push.Changes {
		if ch.New == nil || ch.New.Type != "branch" {
			continue
		}
		commits := make([]commitInfo, 0, len(ch.Commits))
		for _, cm := range ch.Commits {
			name, email := splitAuthor(cm.Author.Raw)
			commits = append(commits, commitInfo{SHA: cm.Hash, Message: cm.Message, URL: cm.Links.HTML.Href, At: cm.Date,
				AuthorLogin: firstNonEmpty(cm.Author.User.name(), name), AuthorKey: cm.Author.User.UUID, AuthorEmail: email, AuthorName: name})
		}
		if err := s.storeCommits(ctx, c, e.Repository.info(), ch.New.Name, commits); err != nil {
			return err
		}
	}
	return nil
}

// onBitbucketStatus records a build's result on the open pull requests from
// its branch, and tells their authors when it failed.
func (s *Service) onBitbucketStatus(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		CommitStatus struct {
			State   string `json:"state"`
			Refname string `json:"refname"`
		} `json:"commit_status"`
		Repository bbRepo `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	result := map[string]string{"SUCCESSFUL": "passing", "FAILED": "failing", "STOPPED": "failing", "INPROGRESS": "pending"}[e.CommitStatus.State]
	if result == "" || e.CommitStatus.Refname == "" {
		return nil
	}
	repoID, err := s.repoFor(ctx, c, e.Repository.info())
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, number, title, html_url, author_login, author_id, checks FROM code_pull_requests
		WHERE repository_id = $1 AND head_branch = $2 AND state IN ('open', 'draft')`, repoID, e.CommitStatus.Refname)
	if err != nil {
		return err
	}
	type found struct {
		pr     storedPull
		before string
	}
	var prs []found
	for rows.Next() {
		f := found{pr: storedPull{provider: c.Provider, repoName: e.Repository.FullName}}
		if rows.Scan(&f.pr.id, &f.pr.number, &f.pr.title, &f.pr.url, &f.pr.author, &f.pr.authorID, &f.before) == nil {
			prs = append(prs, f)
		}
	}
	rows.Close()
	for _, f := range prs {
		if _, err := s.db.Exec(ctx, `UPDATE code_pull_requests SET checks = $2, updated_at = NOW() WHERE id = $1`, f.pr.id, result); err != nil {
			return err
		}
		if result == "failing" && f.before != "failing" {
			pr := f.pr
			s.tell(ctx, c.OrgID, pr.authorID, "code.checks_failed", "A build failed on your pull request",
				fmt.Sprintf("#%d %s in %s", pr.number, pr.title, pr.repoName), &pr)
		}
	}
	return nil
}
