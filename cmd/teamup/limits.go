package main

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/teamup/teamup/internal/ratelimit"
)

// The limits are set well above what a person working does, so nobody ever
// meets one by using TeamUp, and low enough that a script, a stolen session
// or a stranger on the internet cannot flood people, run up the AI or mail
// bill, or keep the database busy. nginx also limits each address; these count
// per person, which nginx cannot see, and hold however many servers there are.

const (
	minute = time.Minute
	hour   = time.Hour
)

func per(n int, d time.Duration) ratelimit.Limit { return ratelimit.Limit{N: n, Per: d} }

// publicLimits apply before anybody is known, by address. An office shares one
// address, so these are about volume a crowd never reaches: an office of 1,000
// averages about 100 API calls a second and reconnects all at once after its
// network drops.
var publicLimits = []ratelimit.Rule{
	// Every API request from one address.
	{Name: "api-address", Prefix: "/api/", Limit: per(15000, minute), Who: ratelimit.ByAddress},

	// Turning a refresh token into a session.
	{Name: "refresh", Method: http.MethodPost, Route: "/api/auth/refresh", Limit: per(120, minute), Who: ratelimit.ByAddress},
	{Name: "login", Method: http.MethodPost, Route: "/api/auth/login", Limit: per(600, minute), Who: ratelimit.ByAddress},

	// Guests: the link is all they have, so a link guessed or shared too widely
	// is the thing to slow.
	{Name: "guest-read", Method: http.MethodGet, Route: "/api/guest/:token", Limit: per(120, minute), Who: ratelimit.ByAddress},
	{Name: "guest-join", Method: http.MethodPost, Route: "/api/guest/:token/join", Limit: per(30, minute), Who: ratelimit.ByAddress},
	{Name: "guest-chat-read", Method: http.MethodGet, Route: "/api/guest/:token/chat", Limit: per(240, minute), Who: ratelimit.ByAddress},
	{Name: "guest-chat", Method: http.MethodPost, Route: "/api/guest/:token/chat", Limit: per(60, minute), Who: ratelimit.ByAddress},

	// Where GitHub, Bitbucket and Google send people back, and where they post.
	{Name: "oauth-return", Method: http.MethodGet, Route: "/api/integrations/github/created", Limit: per(30, minute), Who: ratelimit.ByAddress},
	{Name: "oauth-return", Method: http.MethodGet, Route: "/api/integrations/github/installed/:hookKey", Limit: per(30, minute), Who: ratelimit.ByAddress},
	{Name: "oauth-return", Method: http.MethodGet, Route: "/api/integrations/github/identity/callback", Limit: per(30, minute), Who: ratelimit.ByAddress},
	{Name: "oauth-return", Method: http.MethodGet, Route: "/api/integrations/bitbucket/callback", Limit: per(30, minute), Who: ratelimit.ByAddress},
	{Name: "oauth-return", Method: http.MethodGet, Route: "/api/calendar/google/callback", Limit: per(30, minute), Who: ratelimit.ByAddress},
	{Name: "webhook", Method: http.MethodPost, Route: "/api/integrations/github/hook/:hookKey", Limit: per(1200, minute), Who: ratelimit.ByAddress},
	{Name: "webhook", Method: http.MethodPost, Route: "/api/integrations/bitbucket/hook/:hookKey", Limit: per(1200, minute), Who: ratelimit.ByAddress},
	{Name: "webhook", Method: http.MethodPost, Route: "/api/livekit/webhook", Limit: per(3000, minute), Who: ratelimit.ByAddress},

	// The live connection: opening it, not what goes over it.
	{Name: "ws-open", Method: http.MethodGet, Route: "/ws", Limit: per(1200, minute), Who: ratelimit.ByAddress},
}

// memberLimits apply once somebody is signed in, per person.
var memberLimits = []ratelimit.Rule{
	// Everything one person asks for. Opening the app asks for a few dozen things.
	{Name: "api-person", Limit: per(1200, minute), Who: ratelimit.ByUser},

	// Things that reach other people.
	{Name: "send", Method: http.MethodPost, Route: "/api/channels/:id/messages", Limit: per(120, minute), Who: ratelimit.ByUser},
	{Name: "send", Method: http.MethodPost, Route: "/api/messages/:id/forward", Limit: per(120, minute), Who: ratelimit.ByUser},
	{Name: "edit", Method: http.MethodPatch, Route: "/api/messages/:id", Limit: per(120, minute), Who: ratelimit.ByUser},
	{Name: "react", Method: http.MethodPost, Route: "/api/messages/:id/reactions", Limit: per(240, minute), Who: ratelimit.ByUser},
	{Name: "meeting-chat", Method: http.MethodPost, Route: "/api/calls/:id/chat", Limit: per(120, minute), Who: ratelimit.ByUser},
	{Name: "task-comment", Method: http.MethodPost, Route: "/api/tasks/:id/comments", Limit: per(120, minute), Who: ratelimit.ByUser},
	{Name: "new-channel", Method: http.MethodPost, Route: "/api/channels", Limit: per(60, hour), Who: ratelimit.ByUser},
	{Name: "new-dm", Method: http.MethodPost, Route: "/api/channels/direct", Limit: per(300, hour), Who: ratelimit.ByUser},
	{Name: "add-to-channel", Method: http.MethodPost, Route: "/api/channels/:id/members", Limit: per(300, hour), Who: ratelimit.ByUser},
	{Name: "new-call", Method: http.MethodPost, Route: "/api/calls", Limit: per(120, hour), Who: ratelimit.ByUser},
	{Name: "knock", Method: http.MethodPost, Route: "/api/calls/:id/knock", Limit: per(30, minute), Who: ratelimit.ByUser},
	{Name: "new-task", Method: http.MethodPost, Route: "/api/tasks", Limit: per(600, hour), Who: ratelimit.ByUser},

	// Things that cost money or send mail.
	{Name: "minutes-retry", Method: http.MethodPost, Route: "/api/calls/:id/minutes/retry", Limit: per(10, hour), Who: ratelimit.ByUser},
	{Name: "record", Method: http.MethodPost, Route: "/api/calls/:id/record", Limit: per(30, hour), Who: ratelimit.ByUser},
	{Name: "mail-test", Method: http.MethodPost, Route: "/api/org/mail/test", Limit: per(10, hour), Who: ratelimit.ByUser},
	{Name: "add-member", Method: http.MethodPost, Route: "/api/users", Limit: per(200, hour), Who: ratelimit.ByUser},
	{Name: "import-members", Method: http.MethodPost, Route: "/api/users/import", Limit: per(10, hour), Who: ratelimit.ByUser},
	{Name: "reset-password", Method: http.MethodPost, Route: "/api/users/:id/reset-password", Limit: per(30, hour), Who: ratelimit.ByUser},
	{Name: "change-password", Method: http.MethodPost, Route: "/api/auth/password", Limit: per(10, hour), Who: ratelimit.ByUser},
	{Name: "integration-sync", Method: http.MethodPost, Route: "/api/integrations/:provider/sync", Limit: per(30, hour), Who: ratelimit.ByUser},
	{Name: "calendar-sync", Method: http.MethodPost, Route: "/api/calendar/google/sync", Limit: per(30, hour), Who: ratelimit.ByUser},
	{Name: "access-request", Method: http.MethodPost, Route: "/api/projects/:id/access/requests", Limit: per(30, hour), Who: ratelimit.ByUser},
	{Name: "push-subscribe", Method: http.MethodPost, Route: "/api/notifications/subscribe", Limit: per(30, hour), Who: ratelimit.ByUser},

	// Storage.
	{Name: "upload", Method: http.MethodPost, Route: "/api/files/upload-url", Limit: per(300, hour), Who: ratelimit.ByUser},
	{Name: "upload", Method: http.MethodPost, Route: "/api/files/multipart", Limit: per(300, hour), Who: ratelimit.ByUser},
	{Name: "upload-part", Method: http.MethodPost, Route: "/api/files/multipart/:id/parts", Limit: per(3000, hour), Who: ratelimit.ByUser},
	{Name: "upload", Method: http.MethodPost, Route: "/api/files/record", Limit: per(300, hour), Who: ratelimit.ByUser},
	{Name: "avatar", Method: http.MethodPut, Route: "/api/users/:id/avatar", Limit: per(30, hour), Who: ratelimit.ByUser},
	{Name: "branding", Method: http.MethodPut, Route: "/api/org/branding", Limit: per(30, hour), Who: ratelimit.ByUser},

	// Reads that make the database work.
	{Name: "search", Method: http.MethodGet, Route: "/api/search/messages", Limit: per(120, minute), Who: ratelimit.ByUser},
	{Name: "search", Method: http.MethodGet, Route: "/api/search/files", Limit: per(120, minute), Who: ratelimit.ByUser},
}

// checkLimitRoutes fails start-up when a rule names a route that does not
// exist, so a renamed route can never quietly lose its limit.
func checkLimitRoutes(router *gin.Engine, rules ...[]ratelimit.Rule) error {
	var have []string
	for _, r := range router.Routes() {
		have = append(have, r.Method+" "+r.Path)
	}
	for _, set := range rules {
		for _, want := range ratelimit.Routes(set) {
			if !slices.Contains(have, want) {
				return fmt.Errorf("a rate limit names %s, which is not a route", want)
			}
		}
	}
	return nil
}
