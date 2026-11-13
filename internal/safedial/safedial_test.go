package safedial

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInternalAddressesAreRefusedAndPublicOnesAreNot(t *testing.T) {
	t.Setenv(EnvAllow, "")
	refused := []string{
		"127.0.0.1", "127.8.9.10", "::1", "0.0.0.0", "::",
		"169.254.169.254", "169.254.1.1", "fe80::1",
		"10.0.0.5", "172.16.3.4", "172.31.255.255", "192.168.1.1", "fc00::1", "fd12:3456::1",
		"100.64.0.1", "100.127.255.254",
		"224.0.0.1", "239.255.255.250", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.1.2.3", "::ffff:169.254.169.254",
		"64:ff9b::a00:1", "255.255.255.255",
	}
	for _, s := range refused {
		if err := Check(net.ParseIP(s)); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s should be refused, got %v", s, err)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "172.32.0.1", "100.128.0.1", "2606:4700:4700::1111", "34.120.10.20"}
	for _, s := range allowed {
		if err := Check(net.ParseIP(s)); err != nil {
			t.Errorf("%s is public and should be allowed, got %v", s, err)
		}
	}
}

func TestTheAllowListLetsNamedNetworksAndHostsThrough(t *testing.T) {
	t.Setenv(EnvAllow, " 10.1.0.0/16, 127.0.0.1 ,Model.Internal,fd00::/8")
	for _, s := range []string{"10.1.2.3", "127.0.0.1", "fd00::5"} {
		if err := Check(net.ParseIP(s)); err != nil {
			t.Errorf("%s is in the allow list, got %v", s, err)
		}
	}
	for _, s := range []string{"10.2.0.1", "127.0.0.2", "169.254.169.254"} {
		if err := Check(net.ParseIP(s)); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s is not in the allow list, got %v", s, err)
		}
	}
	if !hostAllowed("model.internal") || !hostAllowed("MODEL.INTERNAL.") || hostAllowed("other.internal") {
		t.Error("hostnames match exactly, ignoring case and a trailing dot")
	}
}

// The check happens when the connection is made, against the address actually
// dialled, so a name that resolves to a public address when it is saved and to
// an internal one later is still refused.
func TestDialingRefusesAnInternalAddressAtConnectTime(t *testing.T) {
	t.Setenv(EnvAllow, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := (&Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("dialling loopback: want ErrBlocked, got %v", err)
	}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	if _, err := (&Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("localhost", port)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("dialling localhost by name: want ErrBlocked, got %v", err)
	}
	if _, err := NewClient(0).Get(srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("an HTTP client: want ErrBlocked, got %v", err)
	}

	t.Setenv(EnvAllow, "127.0.0.1")
	resp, err := NewClient(0).Get(srv.URL)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("allowed: %v %v", resp, err)
	}
	resp.Body.Close()

	t.Setenv(EnvAllow, "localhost")
	conn, err := (&Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("an allowed hostname: %v", err)
	}
	conn.Close()
}

func TestTheHTTPClientDoesNotFollowRedirects(t *testing.T) {
	t.Setenv(EnvAllow, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	resp, err := NewClient(time.Second).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("want the redirect itself back, got %d", resp.StatusCode)
	}
}
