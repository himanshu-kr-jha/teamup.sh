// Package safedial makes outbound connections to addresses an administrator
// typed in, such as a model server or a mail server, without letting those
// addresses reach inside the network TeamUp runs in.
//
// An administrator can name any host, so without this a settings page is a way
// to make the server fetch its own cloud metadata (169.254.169.254), talk to the
// database or Redis next to it, or scan ports on the private network. The check
// is made in net.Dialer.Control, on the address actually being connected to,
// after DNS has been resolved. Checking the name when it is saved would not be
// enough: a name can resolve to a public address then and a private one later
// (DNS rebinding), and every redirect or reconnect resolves it again.
//
// Self-hosters who run a model server or mail relay on their own network list
// it in TEAMUP_OUTBOUND_ALLOW, a comma-separated list of CIDRs, single IP
// addresses or exact host names:
//
//	TEAMUP_OUTBOUND_ALLOW=127.0.0.1,10.0.5.0/24,ollama.internal
//
// A host name in the list is trusted whatever it resolves to; a CIDR or IP is
// matched against the resolved address. The list is read from the environment
// on each connection, so it needs no restart to take effect in tests.
package safedial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// EnvAllow names the environment variable holding the allow list.
const EnvAllow = "TEAMUP_OUTBOUND_ALLOW"

// ErrBlocked is a connection refused because the address is internal and not
// in the allow list. It is not worth retrying: the address will not change.
var ErrBlocked = errors.New("the address is private or local, and TeamUp does not connect to it unless " + EnvAllow + " lists it")

// blocked are the ranges that are not the public internet. The standard
// library's predicates cover loopback, link local, private and multicast; the
// rest are ranges they do not name but that still lead somewhere internal.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT, also used by some cloud VPCs
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, and the broadcast address
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64, which can embed any IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),  // documentation
	netip.MustParsePrefix("100::/64"),       // discard
}

type policy struct {
	prefixes []netip.Prefix
	hosts    map[string]bool
}

var (
	cacheMu  sync.Mutex
	cacheRaw string
	cached   *policy
)

// current is the allow list as the environment says now. Parsing is cached by
// the raw value so a busy server does not parse it on every connection.
func current() *policy {
	raw := os.Getenv(EnvAllow)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cached != nil && raw == cacheRaw {
		return cached
	}
	p := &policy{hosts: map[string]bool{}}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if pfx, err := netip.ParsePrefix(entry); err == nil {
			p.prefixes = append(p.prefixes, pfx.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			p.prefixes = append(p.prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
			continue
		}
		p.hosts[normalHost(entry)] = true
	}
	cacheRaw, cached = raw, p
	return p
}

func normalHost(h string) string { return strings.TrimSuffix(strings.ToLower(h), ".") }

func hostAllowed(host string) bool { return current().hosts[normalHost(host)] }

// Check says whether TeamUp may connect to ip: nil for a public address or one
// in the allow list, ErrBlocked otherwise.
func Check(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w (%q is not an IP address)", ErrBlocked, ip.String())
	}
	addr = addr.Unmap()
	for _, pfx := range current().prefixes {
		if pfx.Contains(addr) {
			return nil
		}
	}
	if internal(addr) {
		return fmt.Errorf("%w (%s)", ErrBlocked, addr)
	}
	return nil
}

func internal(addr netip.Addr) bool {
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsPrivate() {
		return true
	}
	for _, pfx := range blocked {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

// control runs after DNS, just before the socket connects, with the address
// it is about to connect to.
func control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w (%q)", ErrBlocked, address)
	}
	return Check(net.ParseIP(host))
}

// Dialer connects like net.Dialer but refuses internal addresses.
type Dialer struct {
	// Timeout bounds the connection, as net.Dialer.Timeout does. Zero is 30s.
	Timeout time.Duration
}

// DialContext connects to address. A host name in the allow list is
// connected to whatever it resolves to; anything else is checked address by
// address as each connection attempt is made.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	nd := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if host, _, err := net.SplitHostPort(address); err != nil || !hostAllowed(host) {
		nd.Control = control
	}
	return nd.DialContext(ctx, network, address)
}

// NewTransport is http.DefaultTransport's settings with the checked dialer.
// It ignores HTTP(S)_PROXY on purpose: through a proxy the dialer would only
// see the proxy's address, and the destination would go unchecked.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = (&Dialer{}).DialContext
	return t
}

// NewClient is an HTTP client that uses NewTransport and does not follow
// redirects: a redirect is a second destination the administrator never typed,
// and the caller sees it as the 3xx response it is. A zero timeout is none.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
