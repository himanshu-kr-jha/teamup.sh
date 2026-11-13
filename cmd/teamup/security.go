package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// inlineScript is a <script> with a body and no src: the one in index.html
// that sets the theme before the first paint.
var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// scriptHashes are the CSP hashes of the page's inline scripts, worked out from
// the page itself so an edit to it can never leave the policy behind.
func scriptHashes(page []byte) []string {
	var out []string
	for _, m := range inlineScript.FindAllSubmatch(page, -1) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

// origin is scheme://host[:port] of a configured address, or "" if it has none.
// A bare host:port, as the storage endpoint is given, is read with the scheme
// it is reached over.
func origin(raw, scheme string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = scheme + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// contentSecurityPolicy is what the page may load and talk to. Script only from
// this server, and the page's own inline script by its hash, so a message,
// file or name that somehow reached the page as markup still could not run.
// Storage and the call server are named when they live at another address, as
// they do in development; in production both are this server.
//
// GitHub is where connecting it sends a form, so forms may go there too.
func contentSecurityPolicy(page []byte, storage string, storageSecure bool, liveKit, gitHub string) string {
	storageScheme := "http"
	if storageSecure {
		storageScheme = "https"
	}
	files := origin(storage, storageScheme)
	// The call server is configured by one address but reached by the browser
	// over both its socket and its HTTP forms of it.
	calls := origin(liveKit, "ws")
	secure := strings.HasPrefix(calls, "wss://") || strings.HasPrefix(calls, "https://")
	host := calls[strings.Index(calls, "://")+3:]
	callsWS, callsHTTP := "ws://"+host, "http://"+host
	if secure {
		callsWS, callsHTTP = "wss://"+host, "https://"+host
	}
	if calls == "" {
		callsWS, callsHTTP = "", ""
	}
	if gitHub == "" {
		gitHub = "https://github.com"
	}
	forms := origin(gitHub, "https")

	extra := func(list ...string) string {
		var keep []string
		for _, s := range list {
			if s != "" && !slices.Contains(keep, s) {
				keep = append(keep, s)
			}
		}
		return strings.Join(keep, " ")
	}
	directives := []string{
		"default-src 'self'",
		// wasm-unsafe-eval: the background blur's runtime is WebAssembly.
		"script-src " + extra(append([]string{"'self'", "'wasm-unsafe-eval'"}, scriptHashes(page)...)...),
		// React sets style attributes, and the loading screen's style is in the page.
		"style-src 'self' 'unsafe-inline'",
		// Pictures people set, such as a Google or GitHub avatar, live elsewhere.
		"img-src " + extra("'self'", "data:", "blob:", "https:", files),
		"media-src " + extra("'self'", "data:", "blob:", files),
		"font-src 'self' data:",
		// Only this server, its storage and its call server: a script that
		// somehow ran could otherwise send what it read anywhere.
		"connect-src " + extra("'self'", files, callsWS, callsHTTP),
		"worker-src 'self' blob:",
		"frame-src 'self' blob:",
		"manifest-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action " + extra("'self'", forms),
		"frame-ancestors 'self'",
	}
	return strings.Join(directives, "; ")
}

// securityHeaders goes on every answer. A handler that serves something of its
// own, such as an uploaded logo, may set a stricter policy over it.
func securityHeaders(csp string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Calls need the camera, microphone and screen; nothing needs the rest.
		h.Set("Permissions-Policy", "camera=(self), microphone=(self), display-capture=(self), fullscreen=(self), "+
			"geolocation=(), payment=(), usb=(), serial=(), bluetooth=(), hid=(), midi=(), magnetometer=(), gyroscope=(), accelerometer=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		c.Next()
	}
}

// maxBody is the most an API request may carry. Files go straight to storage,
// never through here; the largest thing sent is a members list to import.
const maxBody = 2 << 20

// limitBodies caps what an API request may send, so nobody can make the
// server hold tens of megabytes of JSON in memory at once. The webhooks take
// what GitHub and Bitbucket send, which can be bigger, and cap it themselves.
func limitBodies() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Matched on the route as registered, not on the path's text: a
		// path someone makes up, such as /api/guest/webhookX/join, must not
		// escape the cap by containing the right word.
		if strings.HasPrefix(c.Request.URL.Path, "/api/") && !slices.Contains(webhookRoutes, c.FullPath()) && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
		}
		c.Next()
	}
}

// webhookRoutes take what GitHub, Bitbucket and LiveKit send, and cap it themselves.
var webhookRoutes = []string{
	"/api/integrations/github/hook/:hookKey",
	"/api/integrations/bitbucket/hook/:hookKey",
	"/api/livekit/webhook",
}
