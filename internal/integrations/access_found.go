package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Access somebody already holds on GitHub or Bitbucket is found and recorded,
// so they are not sent to ask for what they have, and admins see the whole
// picture in one place. It is looked for when somebody links an account, when
// a project's repositories change, every few hours, and when an admin asks.
// A grant made from what was found has the source "found"; when that access
// is gone on GitHub or Bitbucket, the grant goes too.

// FindEvery is how often every organization's access is looked at again.
const FindEvery = 6 * time.Hour

// held is one person's access to one repository as the provider says it is:
// "read", "write", or "" for none. ok is false when the provider could not be asked.
type held struct {
	level string
	ok    bool
}

// FindExistingAccess looks for access already held to the organization's
// project repositories. projectID and userID narrow it to one project or one
// person; nil looks at everything.
func (s *Service) FindExistingAccess(ctx context.Context, orgID uuid.UUID, projectID, userID *uuid.UUID) error {
	rows, err := s.db.Query(ctx, `
		SELECT r.project_id, i.user_id, r.id, r.full_name, r.connection_id, i.login, i.external_id
		FROM code_repositories r
		JOIN code_identities i ON i.org_id = r.org_id AND i.provider = r.provider
		JOIN code_connections c ON c.id = r.connection_id AND c.status = 'active'
		JOIN projects p ON p.id = r.project_id AND p.deleted_at IS NULL
		WHERE r.org_id = $1 AND r.present
		  AND ($2::uuid IS NULL OR r.project_id = $2)
		  AND ($3::uuid IS NULL OR i.user_id = $3)
		ORDER BY r.project_id, i.user_id`, orgID, projectID, userID)
	if err != nil {
		return err
	}
	type target struct {
		project, user, repo, conn uuid.UUID
		name, login, externalID   string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.project, &t.user, &t.repo, &t.name, &t.conn, &t.login, &t.externalID); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	conns := map[uuid.UUID]*connection{}
	type key struct{ project, user uuid.UUID }
	found := map[key]map[uuid.UUID]held{}
	var order []key
	for _, t := range targets {
		c, ok := conns[t.conn]
		if !ok {
			if c, err = s.loadConnection(ctx, `id = $1`, t.conn); err != nil {
				return err
			}
			conns[t.conn] = c
		}
		k := key{t.project, t.user}
		if found[k] == nil {
			found[k] = map[uuid.UUID]held{}
			order = append(order, k)
		}
		found[k][t.repo] = s.heldOn(ctx, c, t.name, t.login, t.externalID)
	}
	for _, k := range order {
		if err := s.recordFound(ctx, orgID, k.project, k.user, found[k]); err != nil {
			return err
		}
	}
	return nil
}

// FindAllExistingAccess looks again at every organization with a connection.
func (s *Service) FindAllExistingAccess(ctx context.Context) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT org_id FROM code_connections WHERE status = 'active'`)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not list organizations to look for access")
		return
	}
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			orgs = append(orgs, id)
		}
	}
	rows.Close()
	for _, org := range orgs {
		if err := s.FindExistingAccess(ctx, org, nil, nil); err != nil {
			s.logger.Warn().Err(err).Str("org", org.String()).Msg("could not look for existing access")
		}
	}
}

// findLater looks without holding up whoever made the change.
func (s *Service) findLater(orgID uuid.UUID, projectID, userID *uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := s.FindExistingAccess(ctx, orgID, projectID, userID); err != nil {
			s.logger.Warn().Err(err).Msg("could not look for existing access")
		}
	}()
}

// findAfterLinking looks at what somebody who just linked an account holds,
// so the Projects page already shows it when they get back.
func (s *Service) findAfterLinking(ctx context.Context, orgID, userID uuid.UUID) {
	if err := s.FindExistingAccess(ctx, orgID, nil, &userID); err != nil {
		s.logger.Warn().Err(err).Msg("could not look for existing access after linking")
	}
}

// heldOn asks the provider what one person may do with one repository.
func (s *Service) heldOn(ctx context.Context, c *connection, repo, login, externalID string) held {
	owner, name, _ := strings.Cut(repo, "/")
	switch c.Provider {
	case "github":
		if login == "" {
			return held{}
		}
		token, err := s.installationToken(ctx, c)
		if err != nil {
			return held{}
		}
		var res struct {
			Permission string `json:"permission"`
		}
		path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/collaborators/" + url.PathEscape(login) + "/permission"
		if err := s.gh.do(ctx, "token "+token, http.MethodGet, path, nil, &res); err != nil {
			if isStatus(err, http.StatusNotFound) {
				return held{ok: true}
			}
			s.logger.Warn().Err(err).Str("repo", repo).Msg("could not read access on github")
			return held{}
		}
		return held{level: levelOf(res.Permission), ok: true}
	case "bitbucket":
		if externalID == "" {
			return held{}
		}
		token, _, err := s.ownerToken(ctx, c)
		if err != nil {
			return held{}
		}
		// What they may do through groups as well as by name.
		var list struct {
			Values []struct {
				Permission string `json:"permission"`
			} `json:"values"`
		}
		path := "/workspaces/" + url.PathEscape(owner) + "/permissions/repositories/" + url.PathEscape(name) +
			"?q=" + url.QueryEscape(fmt.Sprintf(`user.uuid="%s"`, externalID))
		if err := s.bbDo(ctx, token, http.MethodGet, path, nil, &list); err == nil {
			level := ""
			for _, v := range list.Values {
				level = higher(level, levelOf(v.Permission))
			}
			return held{level: level, ok: true}
		}
		// Given to them by name, when the fuller list is not offered.
		var one struct {
			Permission string `json:"permission"`
		}
		path = "/repositories/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/permissions-config/users/" + url.PathEscape(externalID)
		if err := s.bbDo(ctx, token, http.MethodGet, path, nil, &one); err != nil {
			if isStatus(err, http.StatusNotFound) {
				return held{ok: true}
			}
			s.logger.Warn().Err(err).Str("repo", repo).Msg("could not read access on bitbucket")
			return held{}
		}
		return held{level: levelOf(one.Permission), ok: true}
	}
	return held{}
}

// levelOf turns a provider's permission into TeamUp's two levels.
func levelOf(permission string) string {
	switch permission {
	case "admin", "maintain", "write", "push":
		return "write"
	case "triage", "read", "pull":
		return "read"
	}
	return ""
}

func higher(a, b string) string {
	if a == "write" || b == "write" {
		return "write"
	}
	if a == "read" || b == "read" {
		return "read"
	}
	return ""
}

// recordFound brings one person's grant for one project in line with what
// the providers say they hold.
func (s *Service) recordFound(ctx context.Context, orgID, projectID, userID uuid.UUID, repos map[uuid.UUID]held) error {
	level, have, known := "", 0, 0
	for _, h := range repos {
		if !h.ok {
			continue
		}
		known++
		if h.level != "" {
			have++
			level = higher(level, h.level)
		}
	}
	if known == 0 {
		return nil // nothing could be asked, so nothing is concluded
	}

	var grantID uuid.UUID
	var source, grantLevel string
	err := s.db.QueryRow(ctx, `SELECT id, source, level FROM project_access_grants WHERE project_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		projectID, userID).Scan(&grantID, &source, &grantLevel)
	hasGrant := err == nil

	switch {
	case hasGrant && source == "teamup":
		// Given here: an invitation that has been accepted shows as given.
		for repo, h := range repos {
			if h.ok && h.level != "" {
				if _, err := s.db.Exec(ctx, `UPDATE project_access_repos SET status = 'granted', detail = '', updated_at = NOW()
					WHERE grant_id = $1 AND repository_id = $2 AND status IN ('invited', 'failed')`, grantID, repo); err != nil {
					return err
				}
			}
		}
		return nil

	case hasGrant && have == 0 && known == len(repos):
		// Found before, and gone everywhere now.
		if _, err := s.db.Exec(ctx, `UPDATE project_access_grants SET revoked_at = NOW() WHERE id = $1`, grantID); err != nil {
			return err
		}
		s.log(ctx, orgID, &projectID, &userID, nil, "", "lost", "")
		return nil

	case !hasGrant && have == 0:
		return nil

	case !hasGrant:
		if err := s.db.QueryRow(ctx, `
			INSERT INTO project_access_grants (org_id, project_id, user_id, level, source) VALUES ($1, $2, $3, $4, 'found')
			ON CONFLICT DO NOTHING RETURNING id`, orgID, projectID, userID, level).Scan(&grantID); err != nil {
			return nil // a grant appeared meanwhile; the next look settles it
		}
		s.log(ctx, orgID, &projectID, &userID, nil, "", "found",
			fmt.Sprintf("%s access to %d of %d repositories", level, have, len(repos)))
	case grantLevel != level && have > 0:
		if _, err := s.db.Exec(ctx, `UPDATE project_access_grants SET level = $2 WHERE id = $1`, grantID, level); err != nil {
			return err
		}
	}

	for repo, h := range repos {
		if !h.ok {
			continue
		}
		status, detail := "existing", h.level
		if h.level == "" {
			status, detail = "none", ""
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO project_access_repos (grant_id, repository_id, status, detail, updated_at) VALUES ($1, $2, $3, $4, NOW())
			ON CONFLICT (grant_id, repository_id) DO UPDATE SET status = EXCLUDED.status, detail = EXCLUDED.detail, updated_at = NOW()`,
			grantID, repo, status, detail); err != nil {
			return err
		}
	}

	// A request this access already answers, on every repository of the
	// project, is closed, and they are told.
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM code_repositories WHERE project_id = $1 AND present`, projectID).Scan(&total); err != nil {
		return err
	}
	if have == total {
		var reqID uuid.UUID
		var asked string
		if s.db.QueryRow(ctx, `SELECT id, level FROM project_access_requests WHERE project_id = $1 AND requester_id = $2 AND status = 'pending'`,
			projectID, userID).Scan(&reqID, &asked) == nil && (asked == level || level == "write") {
			if _, err := s.db.Exec(ctx, `UPDATE project_access_requests SET status = 'approved', decided_at = NOW(),
				note = 'already held on GitHub or Bitbucket' WHERE id = $1`, reqID); err != nil {
				return err
			}
			var projectName string
			_ = s.db.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&projectName)
			s.tellUser(ctx, orgID, userID, "access.granted", fmt.Sprintf("You already have %s access to %s", level, projectName),
				"Your request was closed: your account has this access already.", projectID)
		}
	}
	return nil
}
