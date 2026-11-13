// Command loadtest drives a TeamUp server the way a team at work does, and
// says how fast it answered and how much of it failed.
//
// Each virtual user signs in with its own session, holds a live connection
// open like a browser tab, and every few seconds does what people do most:
// reads a conversation, sends a message, looks at tasks, notifications and
// search. A message sent carries the time it was sent, so the live
// connections of the other members measure how long it took to reach them.
//
//	go run ./tools/loadtest -base https://test-host -users 200 -duration 3m
//
// The password comes from LOADTEST_PASSWORD, so it never sits on a command
// line. Users are load1@<domain> ... loadN@<domain>, as made by
// tools/loadtest/seed.sh.
//
// Every user can be given its own address with -xff, which the test server's
// nginx must be told to trust (set_real_ip_from the generator). Without it
// the whole run comes from one address and meets the per-address limits,
// which are there for strangers, not for a measured test.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

var (
	base      = flag.String("base", "", "server origin, e.g. https://1.2.3.4")
	nUsers    = flag.Int("users", 50, "virtual users")
	first     = flag.Int("first", 1, "number of the first user, so several generators can split the users")
	domain    = flag.String("domain", "loadtest.example", "email domain of the seeded users")
	duration  = flag.Duration("duration", 2*time.Minute, "how long to measure, after everybody has signed in")
	think     = flag.Duration("think", 3*time.Second, "mean pause between one user's actions")
	rampUp    = flag.Duration("ramp", 30*time.Second, "spread the sign-ins over this long")
	sockets   = flag.Int("sockets", 1, "live connections each user holds open (tabs)")
	actions   = flag.Bool("actions", true, "do things; false only holds connections open")
	xff       = flag.Bool("xff", true, "give each user its own address in X-Forwarded-For")
	insecure  = flag.Bool("insecure", true, "accept the test server's self-signed certificate")
	jsonOut   = flag.String("json", "", "also write the summary as JSON to this file")
	label     = flag.String("label", "", "name of this step, carried into the JSON")
	sendShare = flag.Float64("send", 0.15, "share of actions that send a message")
	upShare   = flag.Float64("upload", 0, "share of actions that share a file (0.03 is a busy office)")
	downShare = flag.Float64("download", 0, "share of actions that open a file somebody shared")
)

// stats collects the time each kind of request took.
type stats struct {
	mu      sync.Mutex
	took    map[string][]time.Duration
	status  map[string]map[int]int
	failed  map[string]int
	deliver []time.Duration
}

func newStats() *stats {
	return &stats{took: map[string][]time.Duration{}, status: map[string]map[int]int{}, failed: map[string]int{}}
}

func (s *stats) add(name string, d time.Duration, code int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[name] == nil {
		s.status[name] = map[int]int{}
	}
	if err != nil {
		s.failed[name]++
		return
	}
	s.status[name][code]++
	s.took[name] = append(s.took[name], d)
}

func (s *stats) delivered(d time.Duration) {
	s.mu.Lock()
	s.deliver = append(s.deliver, d)
	s.mu.Unlock()
}

// measuring is off during sign-in and on for the measured window.
var measuring atomic.Bool

var (
	wsOpen, wsFailed, wsDropped atomic.Int64
)

func main() {
	flag.Parse()
	password := os.Getenv("LOADTEST_PASSWORD")
	if *base == "" || password == "" {
		fmt.Fprintln(os.Stderr, "need -base and LOADTEST_PASSWORD")
		os.Exit(2)
	}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: *insecure}, //nolint:gosec // a test server's own certificate
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
	}
	signIn, run := newStats(), newStats()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ready, done sync.WaitGroup
	start := time.Now()
	for i := 0; i < *nUsers; i++ {
		ready.Add(1)
		done.Add(1)
		n := *first + i
		delay := time.Duration(0)
		if *nUsers > 1 {
			delay = *rampUp * time.Duration(i) / time.Duration(*nUsers)
		}
		go func() {
			defer done.Done()
			time.Sleep(delay)
			u := newUser(n, tr)
			ok := u.signIn(ctx, password, signIn)
			ready.Done()
			if !ok {
				return
			}
			for range *sockets {
				go u.live(ctx, run)
			}
			u.work(ctx, run)
		}()
	}
	ready.Wait()
	signedIn := time.Since(start)
	// Let the sockets settle before measuring.
	time.Sleep(5 * time.Second)
	measuring.Store(true)
	measureStart := time.Now()
	time.Sleep(*duration)
	measuring.Store(false)
	window := time.Since(measureStart)
	open := wsOpen.Load()
	cancel()
	waitTimeout(&done, 15*time.Second)

	report(signIn, run, window, signedIn, open)
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	c := make(chan struct{})
	go func() { wg.Wait(); close(c) }()
	select {
	case <-c:
	case <-time.After(d):
	}
}

type user struct {
	n        int
	email    string
	addr     string
	client   *http.Client
	jar      *cookiejar.Jar
	channels []string
	// general is the conversation with everybody in it; it gets a tenth of
	// what a person writes, as an office's company-wide channel does.
	general string
	rng     *rand.Rand
}

func newUser(n int, tr *http.Transport) *user {
	jar, _ := cookiejar.New(nil)
	return &user{
		n:      n,
		email:  fmt.Sprintf("load%d@%s", n, *domain),
		addr:   fmt.Sprintf("10.%d.%d.%d", 100+n/65536%100, n/256%256, n%256),
		jar:    jar,
		client: &http.Client{Transport: tr, Jar: jar, Timeout: 30 * time.Second},
		rng:    rand.New(rand.NewPCG(uint64(n), uint64(time.Now().UnixNano()))),
	}
}

func (u *user) do(ctx context.Context, st *stats, name, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, *base+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if *xff {
		req.Header.Set("X-Forwarded-For", u.addr)
	}
	req.Header.Set("User-Agent", "teamup-loadtest")
	t0 := time.Now()
	res, err := u.client.Do(req)
	if err != nil {
		if ctx.Err() == nil && (st != nil && (measuring.Load() || name == "login")) {
			st.add(name, 0, 0, err)
		}
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if out != nil && res.StatusCode < 300 {
		err = json.NewDecoder(res.Body).Decode(out)
	} else {
		_, _ = io.Copy(io.Discard, res.Body)
	}
	took := time.Since(t0)
	if st != nil && (measuring.Load() || name == "login") {
		st.add(name, took, res.StatusCode, nil)
	}
	return res.StatusCode, err
}

func (u *user) signIn(ctx context.Context, password string, st *stats) bool {
	code, err := u.do(ctx, st, "login", http.MethodPost, "/api/auth/login",
		map[string]string{"email": u.email, "password": password}, nil)
	if err != nil || code != http.StatusOK {
		return false
	}
	var list struct {
		Channels []struct {
			ID   string  `json:"id"`
			Type string  `json:"type"`
			Name *string `json:"name"`
		} `json:"channels"`
	}
	if _, err := u.do(ctx, nil, "", http.MethodGet, "/api/channels", nil, &list); err != nil {
		return false
	}
	for _, c := range list.Channels {
		if c.Name != nil && *c.Name == "general" {
			u.general = c.ID
			continue
		}
		u.channels = append(u.channels, c.ID)
	}
	return true
}

var sentAt = regexp.MustCompile(`lt:(\d{19})`)

// live holds a connection open as a tab does, answers pings, and times the
// messages that arrive.
func (u *user) live(ctx context.Context, st *stats) {
	wsURL := "wss" + strings.TrimPrefix(strings.TrimPrefix(*base, "https"), "http") + "/ws"
	if strings.HasPrefix(*base, "http://") {
		wsURL = "ws" + strings.TrimPrefix(*base, "http") + "/ws"
	}
	h := http.Header{}
	h.Set("User-Agent", "teamup-loadtest")
	if *xff {
		h.Set("X-Forwarded-For", u.addr)
	}
	tr := u.client.Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
	conn, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: tr, Jar: u.jar, Timeout: 30 * time.Second},
		HTTPHeader: h,
	})
	if err != nil {
		if ctx.Err() == nil {
			wsFailed.Add(1)
			if res != nil && os.Getenv("LOADTEST_DEBUG") != "" {
				fmt.Fprintln(os.Stderr, "ws:", res.Status, err)
			}
		}
		return
	}
	conn.SetReadLimit(1 << 20)
	wsOpen.Add(1)
	defer func() {
		wsOpen.Add(-1)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				wsDropped.Add(1)
			}
			return
		}
		if bytes.Contains(data, []byte(`"conn.ping"`)) {
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"conn.pong","payload":{}}`))
			continue
		}
		if !measuring.Load() {
			continue
		}
		if m := sentAt.FindSubmatch(data); m != nil {
			if ns, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil {
				st.delivered(time.Since(time.Unix(0, ns)))
			}
		}
	}
}

// work is one person's day, compressed: an action every few seconds.
func (u *user) work(ctx context.Context, st *stats) {
	if !*actions {
		<-ctx.Done()
		return
	}
	for ctx.Err() == nil {
		pause := time.Duration(u.rng.ExpFloat64() * float64(*think))
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		u.act(ctx, st)
	}
}

func (u *user) channel() string {
	if u.general != "" && (len(u.channels) == 0 || u.rng.Float64() < 0.1) {
		return u.general
	}
	if len(u.channels) == 0 {
		return ""
	}
	return u.channels[u.rng.IntN(len(u.channels))]
}

func (u *user) act(ctx context.Context, st *stats) {
	if f := u.rng.Float64(); f < *upShare {
		u.shareFile(ctx, st)
		return
	} else if f < *upShare+*downShare {
		u.openFile(ctx, st)
		return
	}
	r := u.rng.Float64()
	ch := u.channel()
	switch {
	case r < *sendShare && ch != "":
		body := map[string]string{"content": fmt.Sprintf("Load test message from %d lt:%d", u.n, time.Now().UnixNano())}
		_, _ = u.do(ctx, st, "send message", http.MethodPost, "/api/channels/"+ch+"/messages", body, nil)
	case r < *sendShare+0.35 && ch != "":
		var page struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		}
		_, _ = u.do(ctx, st, "read conversation", http.MethodGet, "/api/channels/"+ch+"/messages", nil, &page)
		if n := len(page.Messages); n > 0 {
			_, _ = u.do(ctx, st, "mark read", http.MethodPost, "/api/channels/"+ch+"/read",
				map[string]string{"messageId": page.Messages[n-1].ID}, nil)
		}
	case r < *sendShare+0.45:
		_, _ = u.do(ctx, st, "list conversations", http.MethodGet, "/api/channels", nil, nil)
	case r < *sendShare+0.55:
		_, _ = u.do(ctx, st, "unread count", http.MethodGet, "/api/notifications/unread-count", nil, nil)
	case r < *sendShare+0.65:
		_, _ = u.do(ctx, st, "list tasks", http.MethodGet, "/api/tasks", nil, nil)
	case r < *sendShare+0.70:
		_, _ = u.do(ctx, st, "task dashboard", http.MethodGet, "/api/tasks/dashboard", nil, nil)
	case r < *sendShare+0.74:
		_, _ = u.do(ctx, st, "create task", http.MethodPost, "/api/tasks",
			map[string]string{"title": fmt.Sprintf("Load test task %d", u.rng.IntN(1e6))}, nil)
	case r < *sendShare+0.78:
		words := []string{"message", "deploy", "review", "meeting", "task"}
		_, _ = u.do(ctx, st, "search", http.MethodGet, "/api/search/messages?q="+url.QueryEscape(words[u.rng.IntN(len(words))]), nil, nil)
	case r < *sendShare+0.82:
		now := time.Now().UTC()
		q := "from=" + url.QueryEscape(now.AddDate(0, 0, -7).Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.AddDate(0, 0, 35).Format(time.RFC3339))
		_, _ = u.do(ctx, st, "calendar", http.MethodGet, "/api/calendar?"+q, nil, nil)
	case r < *sendShare+0.86:
		_, _ = u.do(ctx, st, "calls list", http.MethodGet, "/api/calls", nil, nil)
	case r < *sendShare+0.90:
		_, _ = u.do(ctx, st, "who is online", http.MethodGet, "/api/presence/org/online", nil, nil)
	default:
		_, _ = u.do(ctx, st, "me", http.MethodGet, "/api/auth/me", nil, nil)
	}
}

// --- the summary ---

type line struct {
	Name   string         `json:"name"`
	Count  int            `json:"count"`
	PerSec float64        `json:"perSec"`
	P50    float64        `json:"p50Ms"`
	P95    float64        `json:"p95Ms"`
	P99    float64        `json:"p99Ms"`
	Max    float64        `json:"maxMs"`
	Errors int            `json:"errors"`
	Codes  map[string]int `json:"codes"`
}

type summary struct {
	Label        string  `json:"label"`
	Users        int     `json:"users"`
	SocketsEach  int     `json:"socketsEach"`
	WindowSec    float64 `json:"windowSec"`
	SignInSec    float64 `json:"signInSec"`
	Login        line    `json:"login"`
	Total        line    `json:"total"`
	Endpoints    []line  `json:"endpoints"`
	SocketsOpen  int64   `json:"socketsOpen"`
	SocketsFail  int64   `json:"socketsFailed"`
	SocketsDrop  int64   `json:"socketsDropped"`
	Deliveries   int     `json:"deliveries"`
	DeliverP50   float64 `json:"deliverP50Ms"`
	DeliverP95   float64 `json:"deliverP95Ms"`
	DeliverP99   float64 `json:"deliverP99Ms"`
	ErrorPercent float64 `json:"errorPercent"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func pct(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	i := int(float64(len(ds)-1) * p)
	return ds[i]
}

func summarize(name string, ds []time.Duration, codes map[int]int, failed int, window time.Duration) line {
	ds = slices.Clone(ds)
	slices.Sort(ds)
	l := line{Name: name, Count: len(ds) + failed, Codes: map[string]int{}}
	if window > 0 {
		l.PerSec = float64(l.Count) / window.Seconds()
	}
	l.P50, l.P95, l.P99 = ms(pct(ds, .50)), ms(pct(ds, .95)), ms(pct(ds, .99))
	if len(ds) > 0 {
		l.Max = ms(ds[len(ds)-1])
	}
	l.Errors = failed
	for c, n := range codes {
		l.Codes[strconv.Itoa(c)] = n
		if c >= 400 {
			l.Errors += n
		}
	}
	return l
}

func report(signIn, run *stats, window, signedIn time.Duration, open int64) {
	s := summary{Label: *label, Users: *nUsers, SocketsEach: *sockets, WindowSec: window.Seconds(), SignInSec: signedIn.Seconds()}
	s.Login = summarize("login", signIn.took["login"], signIn.status["login"], signIn.failed["login"], 0)
	var all []time.Duration
	allCodes := map[int]int{}
	allFailed := 0
	names := make([]string, 0, len(run.took))
	for n := range run.status {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s.Endpoints = append(s.Endpoints, summarize(n, run.took[n], run.status[n], run.failed[n], window))
		all = append(all, run.took[n]...)
		for c, k := range run.status[n] {
			allCodes[c] += k
		}
		allFailed += run.failed[n]
	}
	s.Total = summarize("all requests", all, allCodes, allFailed, window)
	if s.Total.Count > 0 {
		s.ErrorPercent = 100 * float64(s.Total.Errors) / float64(s.Total.Count)
	}
	s.SocketsOpen, s.SocketsFail, s.SocketsDrop = open, wsFailed.Load(), wsDropped.Load()
	d := slices.Clone(run.deliver)
	slices.Sort(d)
	s.Deliveries = len(d)
	s.DeliverP50, s.DeliverP95, s.DeliverP99 = ms(pct(d, .5)), ms(pct(d, .95)), ms(pct(d, .99))

	fmt.Printf("\n== %s: %d users, %d sockets each, %.0fs measured (sign-in took %.0fs)\n", s.Label, s.Users, s.SocketsEach, s.WindowSec, s.SignInSec)
	fmt.Printf("login        n=%d p50=%.0fms p95=%.0fms p99=%.0fms errors=%d %v\n", s.Login.Count, s.Login.P50, s.Login.P95, s.Login.P99, s.Login.Errors, s.Login.Codes)
	fmt.Printf("%-20s %7s %7s %8s %8s %8s %8s %6s\n", "request", "count", "per s", "p50 ms", "p95 ms", "p99 ms", "max ms", "errors")
	for _, l := range append(s.Endpoints, s.Total) {
		fmt.Printf("%-20s %7d %7.1f %8.1f %8.1f %8.1f %8.0f %6d\n", l.Name, l.Count, l.PerSec, l.P50, l.P95, l.P99, l.Max, l.Errors)
	}
	fmt.Printf("codes %v, error %.2f%%\n", s.Total.Codes, s.ErrorPercent)
	if up, down := bytesUp.Load(), bytesDown.Load(); up+down > 0 {
		fmt.Printf("files: %.0f MB up, %.0f MB down (%.1f / %.1f MB/s over the whole run)\n",
			float64(up)/1e6, float64(down)/1e6, float64(up)/1e6/window.Seconds(), float64(down)/1e6/window.Seconds())
	}
	fmt.Printf("sockets open=%d failed=%d dropped=%d; message delivery n=%d p50=%.0fms p95=%.0fms p99=%.0fms\n",
		s.SocketsOpen, s.SocketsFail, s.SocketsDrop, s.Deliveries, s.DeliverP50, s.DeliverP95, s.DeliverP99)
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(s, "", "  ")
		_ = os.WriteFile(*jsonOut, b, 0o644)
	}
}

// --- files ---

// payload is what uploads send: random, so nothing on the way compresses it.
var payload = func() []byte {
	b := make([]byte, 16<<20)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}()

// shared is every file uploaded so far, by conversation, for others to open.
var shared = struct {
	sync.Mutex
	by map[string][]sharedFile
}{by: map[string][]sharedFile{}}

type sharedFile struct{ key, name string }

var bytesUp, bytesDown atomic.Int64

// fileSize is the mix people send: mostly pictures and screenshots, some
// documents, now and then something large.
func (u *user) fileSize() (int, string, string) {
	switch r := u.rng.Float64(); {
	case r < 0.70:
		return 150<<10 + u.rng.IntN(250<<10), "screenshot.png", "image/png"
	case r < 0.95:
		return 1<<20 + u.rng.IntN(3<<20), "notes.pdf", "application/pdf"
	default:
		return 10<<20 + u.rng.IntN(5<<20), "recording.mp4", "video/mp4"
	}
}

// shareFile does what the app does: asks for a signed address, sends the bytes
// straight to storage through nginx, records the file and sends it in a message.
func (u *user) shareFile(ctx context.Context, st *stats) {
	ch := u.channel()
	if ch == "" {
		return
	}
	size, name, ctype := u.fileSize()
	t0 := time.Now()
	var link struct {
		UploadURL string            `json:"uploadUrl"`
		ObjectKey string            `json:"objectKey"`
		Headers   map[string]string `json:"headers"`
	}
	code, err := u.do(ctx, st, "upload link", http.MethodPost, "/api/files/upload-url", map[string]any{
		"category": "chat", "refId": ch, "filename": name, "contentType": ctype, "size": size}, &link)
	if err != nil || code != http.StatusOK || link.UploadURL == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, link.UploadURL, bytes.NewReader(payload[:size]))
	if err != nil {
		return
	}
	for k, v := range link.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", ctype)
	}
	if *xff {
		req.Header.Set("X-Forwarded-For", u.addr)
	}
	req.ContentLength = int64(size)
	if !u.raw(st, "upload bytes", req, nil) {
		return
	}
	bytesUp.Add(int64(size))
	var att struct {
		ID string `json:"id"`
	}
	code, err = u.do(ctx, st, "record file", http.MethodPost, "/api/files/record", map[string]any{
		"category": "chat", "refId": ch, "objectKey": link.ObjectKey, "filename": name, "contentType": ctype, "size": size}, &att)
	if err != nil || code >= 300 || att.ID == "" {
		return
	}
	_, _ = u.do(ctx, st, "send with file", http.MethodPost, "/api/channels/"+ch+"/messages",
		map[string]any{"content": "", "attachmentIds": []string{att.ID}}, nil)
	if measuring.Load() {
		st.add("share file (whole)", time.Since(t0), http.StatusOK, nil)
	}
	shared.Lock()
	list := append(shared.by[ch], sharedFile{link.ObjectKey, name})
	if len(list) > 200 {
		list = list[len(list)-200:]
	}
	shared.by[ch] = list
	shared.Unlock()
}

// openFile downloads a file somebody shared in one of this user's conversations.
func (u *user) openFile(ctx context.Context, st *stats) {
	ch := u.channel()
	shared.Lock()
	list := shared.by[ch]
	var f sharedFile
	if len(list) > 0 {
		f = list[u.rng.IntN(len(list))]
	}
	shared.Unlock()
	if f.key == "" {
		return
	}
	var link struct {
		DownloadURL string `json:"downloadUrl"`
	}
	q := "objectKey=" + url.QueryEscape(f.key) + "&filename=" + url.QueryEscape(f.name)
	code, err := u.do(ctx, st, "download link", http.MethodGet, "/api/files/download-url?"+q, nil, &link)
	if err != nil || code != http.StatusOK || link.DownloadURL == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.DownloadURL, nil)
	if err != nil {
		return
	}
	if *xff {
		req.Header.Set("X-Forwarded-For", u.addr)
	}
	var n int64
	if u.raw(st, "download bytes", req, &n) {
		bytesDown.Add(n)
	}
}

// raw sends a request to a signed storage address and times it, body and all.
func (u *user) raw(st *stats, name string, req *http.Request, read *int64) bool {
	t0 := time.Now()
	res, err := u.client.Do(req)
	if err != nil {
		if req.Context().Err() == nil && measuring.Load() {
			st.add(name, 0, 0, err)
		}
		return false
	}
	n, _ := io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if read != nil {
		*read = n
	}
	if measuring.Load() {
		st.add(name, time.Since(t0), res.StatusCode, nil)
	}
	if res.StatusCode >= 300 && os.Getenv("LOADTEST_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, name, res.Status)
	}
	return res.StatusCode < 300
}
