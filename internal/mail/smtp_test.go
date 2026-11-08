package mail

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is just enough of an SMTP server to tell what a client really did:
// whether it encrypted, which password it sent and when, and what it delivered.
type fakeServer struct {
	t         *testing.T
	listener  net.Listener
	tls       *tls.Config
	implicit  bool // TLS from the first byte, as on port 465
	starttls  bool // offers STARTTLS
	password  string
	mu        sync.Mutex
	authClear bool // a password arrived before the connection was encrypted
	authed    bool
	from, to  string
	data      string
}

func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"mail.test"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, pool
}

func startFake(t *testing.T, implicit, starttls bool, password string) (*fakeServer, Settings) {
	t.Helper()
	serverTLS, pool := selfSigned(t)
	f := &fakeServer{t: t, tls: serverTLS, implicit: implicit, starttls: starttls, password: password}
	var err error
	if implicit {
		f.listener, err = tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	} else {
		f.listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.listener.Close() })
	go f.serve()

	port := f.listener.Addr().(*net.TCPAddr).Port
	oldTLS, oldImplicit := tlsConfig, implicitTLS
	tlsConfig = func(host string) *tls.Config { return &tls.Config{ServerName: host, RootCAs: pool} }
	implicitTLS = func(p int) bool { return implicit && p == port }
	t.Cleanup(func() { tlsConfig, implicitTLS = oldTLS, oldImplicit })
	return f, Settings{Host: "127.0.0.1", Port: port, User: "teamup", Pass: password, From: "TeamUp <teamup@example.test>"}
}

func (f *fakeServer) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.session(conn)
	}
}

func (f *fakeServer) session(conn net.Conn) {
	defer conn.Close()
	encrypted := f.implicit
	r, w := bufio.NewReader(conn), conn
	say := func(line string) { _, _ = w.Write([]byte(line + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			say("250-fake")
			if f.starttls && !encrypted {
				say("250-STARTTLS")
			}
			say("250 AUTH PLAIN")
		case upper == "STARTTLS":
			say("220 go ahead")
			tlsConn := tls.Server(conn, f.tls)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn, encrypted = tlsConn, true
			r, w = bufio.NewReader(tlsConn), tlsConn
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			f.mu.Lock()
			if !encrypted {
				f.authClear = true
			}
			ok := len(parts) == 3 && parts[2] == f.password
			f.authed = ok
			f.mu.Unlock()
			if ok {
				say("235 ok")
			} else {
				say("535 5.7.8 Username and Password not accepted")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.Trim(cmd[len("MAIL FROM:"):], "<> ")
			f.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			f.mu.Lock()
			f.to = strings.Trim(cmd[len("RCPT TO:"):], "<> ")
			f.mu.Unlock()
			say("250 ok")
		case upper == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case upper == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeServer) delivered() (string, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.from, f.to, f.data
}

func TestAMessageGoesThroughStartTLSWithANamedSender(t *testing.T) {
	f, settings := startFake(t, false, true, "secret")
	if err := send(settings, Message{To: "ada@example.test", Subject: "Hi", Body: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	from, to, data := f.delivered()
	if from != "teamup@example.test" || to != "ada@example.test" {
		t.Errorf("envelope %q -> %q; a From with a name still gives the bare address", from, to)
	}
	if !strings.Contains(data, "From: TeamUp <teamup@example.test>") || !strings.Contains(data, "hello") {
		t.Errorf("message:\n%s", data)
	}
	if f.authClear {
		t.Error("the password was sent before encrypting")
	}
}

func TestPort465SpeaksTLSFromTheStart(t *testing.T) {
	f, settings := startFake(t, true, false, "secret")
	if err := send(settings, Message{To: "ada@example.test", Subject: "Hi", Body: "hello"}); err != nil {
		t.Fatalf("send over implicit TLS: %v", err)
	}
	if _, to, _ := f.delivered(); to != "ada@example.test" {
		t.Errorf("not delivered, got %q", to)
	}
}

func TestAPasswordIsNeverSentToARemoteServerWithoutEncryption(t *testing.T) {
	f, settings := startFake(t, false, false, "secret")
	// The same server reached by a name that is not this machine.
	settings.Host = "mail.test"
	old := dialAddress
	dialAddress = func(string, int) string { return f.listener.Addr().String() }
	t.Cleanup(func() { dialAddress = old })

	err := send(settings, Message{To: "ada@example.test", Subject: "Hi", Body: "hello"})
	if err == nil || !strings.Contains(err.Error(), "no encrypted connection") {
		t.Fatalf("want a refusal that says why, got %v", err)
	}
	if f.authClear || f.authed {
		t.Error("the password went over a plain connection")
	}
}

func TestAWrongPasswordReportsTheServersReason(t *testing.T) {
	_, settings := startFake(t, false, true, "secret")
	settings.Pass = "wrong"
	err := send(settings, Message{To: "ada@example.test", Subject: "Hi", Body: "hello"})
	if err == nil || !strings.Contains(err.Error(), "Username and Password not accepted") {
		t.Fatalf("want the server's words, got %v", err)
	}
}

func TestAServerThatIsDownSaysItCouldNotBeReached(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	err = send(Settings{Host: "127.0.0.1", Port: port, From: "a@example.test"}, Message{To: "b@example.test"})
	if err == nil || !strings.Contains(err.Error(), "could not reach 127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("got %v", err)
	}
}

func TestSettingsAreCheckedInWordsAnAdministratorCanActOn(t *testing.T) {
	good := Settings{Host: "smtp.gmail.com", Port: 587, From: "TeamUp <team@example.com>"}
	if err := good.Validate(); err != nil {
		t.Errorf("good settings refused: %v", err)
	}
	for name, s := range map[string]Settings{
		"no host":          {Port: 587, From: "a@example.com"},
		"host with scheme": {Host: "smtp://smtp.gmail.com", From: "a@example.com"},
		"port too big":     {Host: "smtp.gmail.com", Port: 70000, From: "a@example.com"},
		"from not email":   {Host: "smtp.gmail.com", From: "TeamUp"},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
