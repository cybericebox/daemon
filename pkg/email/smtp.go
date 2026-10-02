package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// Timeouts of one send: the connection, and the whole conversation. A server that stops answering (a tarpit)
// is let go when the conversation deadline passes; the context ends it sooner.
const (
	dialTimeout    = 15 * time.Second
	sessionTimeout = 60 * time.Second
)

// Failure kinds of a send. The text of SendError is one fixed line per kind: the raw dial error and the
// server's banner never leave the process, they are logged.
const (
	FailureConnect         = "connect"
	FailureTLS             = "tls"
	FailureTLSRequired     = "tls_required"
	FailureBlocked         = "blocked_destination"
	FailureAuth            = "auth"
	FailureRejected        = "rejected"
	FailureProtocol        = "protocol"
	failureTextConnect     = "email: SMTP connection failed: timeout or unreachable"
	failureTextTLS         = "email: SMTP TLS failed: tls: handshake"
	failureTextTLSRequired = "email: SMTP server does not offer TLS: tls: required"
	failureTextBlocked     = "email: SMTP destination is not allowed"
)

// SendError is a failed send. Its text is generic; Unwrap gives the raw error to errors.As (an SMTP reply
// code, a network error).
type SendError struct {
	Kind string
	text string
	err  error
}

func (e *SendError) Error() string        { return e.text }
func (e *SendError) Unwrap() error        { return e.err }
func (e *SendError) Is(target error) bool { return target == ErrSendFailed }

func newSendError(kind string, raw error) *SendError {
	e := &SendError{Kind: kind, err: raw}
	switch kind {
	case FailureConnect:
		e.text = failureTextConnect
	case FailureTLS:
		e.text = failureTextTLS
	case FailureTLSRequired:
		e.text = failureTextTLSRequired
	case FailureBlocked:
		e.text = failureTextBlocked
	default:
		e.text = "email: SMTP send failed"
	}
	return e
}

// classify turns what the SMTP conversation returned into a SendError.
func classify(err error) *SendError {
	var blocked *blockedError
	if errors.As(err, &blocked) {
		return newSendError(FailureBlocked, err)
	}
	var tlsErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	if errors.As(err, &tlsErr) || errors.As(err, &recordErr) {
		return newSendError(FailureTLS, err)
	}
	var reply *textproto.Error
	if errors.As(err, &reply) {
		e := &SendError{err: err}
		switch reply.Code {
		case 530, 534, 535, 538:
			e.Kind = FailureAuth
		default:
			e.Kind = FailureRejected
		}
		e.text = fmt.Sprintf("email: SMTP server replied %d", reply.Code)
		return e
	}
	var tlsAlert tls.AlertError
	if errors.As(err, &tlsAlert) || strings.Contains(err.Error(), "tls:") {
		return newSendError(FailureTLS, err)
	}
	return newSendError(FailureConnect, err)
}

// blockedError is returned by the dial guard for an address that is not allowed.
type blockedError struct{ addr string }

func (e *blockedError) Error() string { return "destination " + e.addr + " is not allowed" }

// blockedPrefixes are address ranges a guarded connection never opens: not covered by netip's own
// predicates (carrier-grade NAT, benchmarking, the "this network", reserved and IETF-protocol ranges).
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// AddressBlocked reports whether ip is an address a guarded connection must not reach: loopback, private,
// link-local (the cloud metadata address included), carrier-grade NAT, multicast, unspecified and the other
// special ranges.
func AddressBlocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// guardControl is the net.Dialer control of a guarded connection: it sees the address that is about to be
// connected (after name resolution), so a name that resolves to a blocked address, or changes its answer
// between a check and the connection, never gets a connection.
func guardControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return &blockedError{addr: address}
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || AddressBlocked(ip) {
		return &blockedError{addr: host}
	}
	return nil
}

// deliver sends one message over its own connection, with deadlines on every step, the connection secured
// before any credential is sent (unless the transport is marked insecure), and a guarded destination when
// asked.
func (c *Client) deliver(ctx context.Context, from, to string, data []byte) error {
	err := c.converse(ctx, from, to, data)
	if err == nil {
		return nil
	}
	sendErr := classify(err)
	log.Warn().Err(err).Str("host", c.host).Int("port", c.port).Str("kind", sendErr.Kind).Msg("SMTP send failed")
	return sendErr
}

func (c *Client) converse(ctx context.Context, from, to string, data []byte) error {
	dialer := &net.Dialer{Timeout: dialTimeout}
	if c.guard {
		dialer.Control = guardControl
	}
	deadline := time.Now().Add(sessionTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.host, fmt.Sprint(c.port)))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(deadline)
	// A cancelled context closes the connection, which ends any blocked read or write.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	tlsConfig := &tls.Config{ServerName: c.host, MinVersion: tls.VersionTLS12}
	secured := false
	if c.implicitTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		conn, secured = tlsConn, true
	}
	client, err := smtp.NewClient(conn, c.host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err = client.Hello("localhost"); err != nil {
		return err
	}
	if !secured {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(tlsConfig); err != nil {
				return err
			}
			secured = true
		} else if !c.insecure {
			return errTLSRequired
		}
	}
	if c.username != "" {
		if !secured && !c.insecure {
			return errTLSRequired
		}
		if err = client.Auth(c.auth(client)); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = bytes.NewReader(data).WriteTo(w); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// errTLSRequired: the server does not offer STARTTLS and the transport does not accept an unencrypted session.
var errTLSRequired = &requiredError{}

type requiredError struct{}

func (*requiredError) Error() string { return "tls: STARTTLS is required but not offered" }

// auth picks PLAIN when the server offers it, else LOGIN.
func (c *Client) auth(client *smtp.Client) smtp.Auth {
	_, mechanisms := client.Extension("AUTH")
	if strings.Contains(strings.ToUpper(mechanisms), "PLAIN") || !strings.Contains(strings.ToUpper(mechanisms), "LOGIN") {
		return &plainAuth{user: c.username, password: c.password}
	}
	return &loginAuth{user: c.username, password: c.password}
}

type plainAuth struct{ user, password string }

func (a *plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.password), nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

// loginAuth is AUTH LOGIN, still the only mechanism some providers offer.
type loginAuth struct{ user, password string }

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a *loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected server challenge")
}
