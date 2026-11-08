package auth

// Permissions: what a role lets somebody do beyond their own work. A role is
// a set of these, chosen from the list below on the Roles screen, and what the
// server checks is the permission, never the role's name, so a role an
// organization makes up works exactly as its boxes say.
//
// Everybody can do their own work without any: chat in their channels, join
// calls they are invited to, run the tasks assigned to them. Managers also
// get what the org chart gives them over their own teams, which is not a
// permission because it depends on whose team it is.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// Every permits everything, now and whatever is added later. Only the owner
// role has it.
const Every = "*"

// The permissions.
const (
	PermOrgSettings   = "org.settings"
	PermMembers       = "members.manage"
	PermRoles         = "roles.manage"
	PermTeams         = "teams.manage"
	PermTasksAll      = "tasks.all"
	PermChannelsAll   = "channels.all"
	PermMeetingsAll   = "meetings.all"
	PermCallsRecord   = "calls.record"
	PermNotesSettings = "notes.settings"
	PermNotesReadAll  = "notes.read_all"
)

// Permission is one entry on the Roles screen.
type Permission struct {
	Key         string `json:"key"`
	Area        string `json:"area"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Catalog is every permission, in the order the Roles screen shows them.
var Catalog = []Permission{
	{PermOrgSettings, "Organization", "Change organization settings", "The organization's name, its email server, colour and logo."},
	{PermMembers, "Organization", "Manage members", "Add, import, edit and remove members, reset their passwords and change their pictures."},
	{PermRoles, "Organization", "Manage roles", "Create and change roles, and give people roles. Nobody can grant a permission they do not have."},
	{PermTeams, "Organization", "Manage all teams", "Delete teams and change who is on any team, not only teams they run."},
	{PermTasksAll, "Work", "See and change everybody's tasks", "Every task in the organization, and the whole organization's dashboard."},
	{PermChannelsAll, "Work", "Administer every channel", "Manage any channel and post where only its administrators can."},
	{PermMeetingsAll, "Meetings", "Manage everybody's meetings", "Change or cancel anybody's meeting, let guests in, end or mute in any call, and read any call's log and attendance."},
	{PermCallsRecord, "Meetings", "Record calls", "Start and stop recording a call they are in."},
	{PermNotesSettings, "Meetings", "Set up meeting notes", "Turn the note-taker on or off and choose its services and keys."},
	{PermNotesReadAll, "Meetings", "Read every meeting's minutes", "Minutes and transcripts of meetings they were not in."},
}

// IsPermission says whether key is on the list.
func IsPermission(key string) bool {
	return slices.ContainsFunc(Catalog, func(p Permission) bool { return p.Key == key })
}

// AllPermissions is every key on the list.
func AllPermissions() []string {
	out := make([]string, len(Catalog))
	for i, p := range Catalog {
		out[i] = p.Key
	}
	return out
}

// DefaultRolePermissions is what the built-in roles start with.
func DefaultRolePermissions() []struct {
	Name        string
	Permissions []string
} {
	return []struct {
		Name        string
		Permissions []string
	}{
		{"owner", []string{Every}},
		{"admin", AllPermissions()},
		{"manager", []string{PermCallsRecord}},
		{"member", []string{}},
	}
}

// legacyPermissions is what a session carries when nothing worked out its
// permissions from the database, as in a test that names roles directly: the
// built-in roles' defaults.
func legacyPermissions(roles []string) []string {
	var out []string
	for _, d := range DefaultRolePermissions() {
		if slices.Contains(roles, d.Name) {
			out = append(out, d.Permissions...)
		}
	}
	return out
}

// Can says whether the signed-in person holds a permission through any of
// their roles.
func (u *UserContext) Can(perm string) bool {
	perms := u.Permissions
	if perms == nil {
		perms = legacyPermissions(u.Roles)
	}
	return slices.Contains(perms, Every) || slices.Contains(perms, perm)
}

// Held is every permission the person holds, as Can reads them.
func (u *UserContext) Held() []string {
	if u.Permissions == nil {
		return legacyPermissions(u.Roles)
	}
	return u.Permissions
}

// Grants says whether somebody holding held may hand on perm: they hold it
// themselves, or hold everything.
func Grants(held []string, perm string) bool {
	return slices.Contains(held, Every) || slices.Contains(held, perm)
}

// IsOwner is the owner role itself, for the few things only an owner may do,
// such as resetting another owner's password.
func (u *UserContext) IsOwner() bool { return u.HasRole("owner") }

// ── working out a person's permissions ─────────────────────────────────────

// PermissionsOf is the union of the permissions of somebody's roles, as they
// are now in the database: not as they were when they signed in, and not
// cached, so giving or taking away a role takes effect on the next request on
// every replica. It is one indexed query.
func (s *Service) PermissionsOf(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.permissions FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("read permissions: %w", err)
	}
	defer rows.Close()
	perms := []string{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			continue
		}
		for _, p := range list {
			if !slices.Contains(perms, p) {
				perms = append(perms, p)
			}
		}
	}
	return perms, rows.Err()
}
