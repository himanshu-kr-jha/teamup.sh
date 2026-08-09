package main

import "testing"

// The LiveKit address is written as a websocket address by some setups and an
// http one by others. Both name the same host, and the proxy needs an http
// scheme either way.
func TestLiveKitProxyTarget(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"ws://localhost:7880", "http://localhost:7880"},
		{"http://localhost:7880", "http://localhost:7880"},
		{"wss://livekit.example.com", "https://livekit.example.com"},
		{"https://livekit.example.com", "https://livekit.example.com"},
		{"ws://livekit:7880", "http://livekit:7880"},
	}
	for _, c := range cases {
		got := liveKitProxyTarget(c.raw)
		if got == nil {
			t.Errorf("%q: got nothing", c.raw)
			continue
		}
		if got.String() != c.want {
			t.Errorf("%q: got %q want %q", c.raw, got.String(), c.want)
		}
	}
}

// Nothing configured, or something that is not an address, means no proxy —
// rather than a route that fails in a confusing way at request time.
func TestLiveKitProxyTargetRefusesNonsense(t *testing.T) {
	for _, raw := range []string{"", "not a url", "ftp://livekit:7880", "://broken"} {
		if got := liveKitProxyTarget(raw); got != nil {
			t.Errorf("%q should not produce a target, got %q", raw, got)
		}
	}
}
