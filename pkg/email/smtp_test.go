package email

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a tiny plain-text SMTP server: it records the commands it saw and offers PLAIN auth.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	commands []string
	data     string
	silent   bool // accepts and never speaks (a tarpit)
}

func newFakeSMTP(t *testing.T, silent bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, silent: silent}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if f.silent {
		time.Sleep(5 * time.Second)
		return
	}
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	say("220 fake ready")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		if inData {
			if line == "." {
				inData = false
				f.mu.Unlock()
				say("250 queued")
				continue
			}
			f.data += line + "\n"
			f.mu.Unlock()
			continue
		}
		f.commands = append(f.commands, line)
		f.mu.Unlock()
		switch verb := strings.ToUpper(strings.Fields(line + " x")[0]); verb {
		case "EHLO":
			say("250-fake")
			say("250 AUTH PLAIN")
		case "AUTH":
			say("235 ok")
		case "DATA":
			inData = true
			say("354 go")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func clientFor(f *fakeSMTP, mutate func(*Config)) *Client {
	cfg := Config{Host: "127.0.0.1", Port: f.port(), Username: "u", Password: "secret-pass", TLS: TLSModeStartTLS, SenderEmail: "a@example.test"}
	mutate(&cfg)
	c, _ := New(cfg)
	return c
}

func TestAddressBlockedCoversTheInternalRanges(t *testing.T) {
	for addr, blocked := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.16.0.9": true, "192.168.1.1": true, "169.254.169.254": true,
		"100.64.0.1": true, "0.0.0.0": true, "224.0.0.1": true, "::1": true, "fe80::1": true, "fd00::1": true,
		"::ffff:10.0.0.1": true, "198.18.0.1": true, "255.255.255.255": true,
		"8.8.8.8": false, "1.1.1.1": false, "2606:4700:4700::1111": false,
	} {
		if got := AddressBlocked(netip.MustParseAddr(addr)); got != blocked {
			t.Errorf("%s blocked = %v, want %v", addr, got, blocked)
		}
	}
}

func TestAGuardedTransportNeverConnectsToAnInternalAddress(t *testing.T) {
	f := newFakeSMTP(t, false)
	c := clientFor(f, func(cfg *Config) { cfg.GuardDial = true; cfg.Insecure = true })
	err := c.SendMessage(context.Background(), Message{To: "x@example.test", Subject: "s", HTML: "<p>x</p>"})
	var sendErr *SendError
	if !errors.As(err, &sendErr) || sendErr.Kind != FailureBlocked || !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v", err)
	}
	if len(f.seen()) != 0 {
		t.Fatalf("the server must not see a connection: %v", f.seen())
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "refused") {
		t.Fatalf("the error text must stay generic: %q", err.Error())
	}
}

func TestStartTLSIsRequiredAndNoCredentialGoesInTheClear(t *testing.T) {
	f := newFakeSMTP(t, false) // offers no STARTTLS
	c := clientFor(f, func(*Config) {})
	err := c.SendMessage(context.Background(), Message{To: "x@example.test", Subject: "s", HTML: "<p>x</p>"})
	var sendErr *SendError
	if !errors.As(err, &sendErr) || sendErr.Kind != FailureTLS && sendErr.Kind != FailureTLSRequired {
		t.Fatalf("err = %v", err)
	}
	for _, line := range f.seen() {
		if strings.HasPrefix(strings.ToUpper(line), "AUTH") || strings.Contains(line, "secret-pass") {
			t.Fatalf("credentials were sent without TLS: %q", line)
		}
	}
}

func TestAnInsecureTransportMayTalkPlainAndDelivers(t *testing.T) {
	f := newFakeSMTP(t, false)
	c := clientFor(f, func(cfg *Config) { cfg.Insecure = true })
	if err := c.SendMessage(context.Background(), Message{To: "x@example.test", Subject: "hello", HTML: "<p>x</p>"}); err != nil {
		t.Fatal(err)
	}
	var sawAuth, sawRcpt bool
	for _, line := range f.seen() {
		sawAuth = sawAuth || strings.HasPrefix(line, "AUTH PLAIN")
		sawRcpt = sawRcpt || strings.HasPrefix(line, "RCPT TO:<x@example.test>")
	}
	if !sawAuth || !sawRcpt || !strings.Contains(f.data, "Subject: hello") {
		t.Fatalf("commands %v data %q", f.seen(), f.data)
	}
}

func TestATarpitIsLetGoWhenTheContextEnds(t *testing.T) {
	f := newFakeSMTP(t, true)
	c := clientFor(f, func(cfg *Config) { cfg.Insecure = true })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.SendMessage(ctx, Message{To: "x@example.test", Subject: "s", HTML: "<p>x</p>"})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("a silent server must not hold the sender: err=%v after %v", err, time.Since(start))
	}
	var sendErr *SendError
	if !errors.As(err, &sendErr) || sendErr.Kind != FailureConnect {
		t.Fatalf("err = %v", err)
	}
}
