package netlink

import (
	"io"
	"time"

	"github.com/hybridgroup/tls13"
)

// tlsConn is the part of tls13.Conn used here. Going through an interface
// keeps tls13 out of the binary unless UseTLS is called.
type tlsConn interface {
	Configure(tls13.Config) error
	Open(io.ReadWriter) error
	Handshake() error
	io.ReadWriteCloser
}

// tlsSocket runs one TLS client over a TCP socket of the Berkeley stack.
type tlsSocket struct {
	n        *Esplink
	cfg      *tls13.Config
	conn     tlsConn
	fd       int
	open     bool
	deadline time.Time
}

func (s *tlsSocket) Read(b []byte) (int, error) {
	return s.n.berkeley.Recv(s.fd, b, 0, s.deadline)
}

func (s *tlsSocket) Write(b []byte) (int, error) {
	return s.n.berkeley.Send(s.fd, b, 0, s.deadline)
}

// UseTLS makes crypto/tls.Dial and https requests on n use tls13 with cfg, which fail without it.
// ServerName is set to the dialed host. One TLS connection can be open at a time.
func UseTLS(n *Esplink, cfg *tls13.Config) {
	n.tls.n = n
	n.tls.cfg = cfg
	if n.tls.conn == nil {
		n.tls.conn = new(tls13.Conn)
	}
}

func (n *Esplink) tlsSocket(protocol int) error {
	if protocol != ipprotoTLS {
		return nil
	}
	if n.tls.cfg == nil {
		return errNoTLSConfig
	}
	if n.tls.open {
		return errTLSBusy
	}
	return nil
}

func (n *Esplink) isTLS(fd int) bool {
	return n.tls.open && fd == n.tls.fd
}

func (n *Esplink) tlsHandshake(host string) error {
	cfg := *n.tls.cfg
	cfg.ServerName = host
	n.tls.deadline = time.Time{}
	if n.TLSHandshakeTimeout > 0 {
		n.tls.deadline = time.Now().Add(n.TLSHandshakeTimeout)
	}
	if err := n.tls.conn.Configure(cfg); err != nil {
		return err
	}
	if err := n.tls.conn.Open(&n.tls); err != nil {
		return err
	}
	start := time.Now()
	err := n.tls.conn.Handshake()
	if debug {
		println("TLS handshake with", host, "took", time.Since(start).String())
	}
	return err
}

// tlsResult releases the TLS socket when err is not nil or io.EOF. A failed
// connection cannot be used again, and net/http does not always close it.
func (n *Esplink) tlsResult(nb int, err error) (int, error) {
	if err != nil && err != io.EOF {
		if debug {
			println("TLS failed:", err.Error())
		}
		n.tls.conn.Close()
		n.tls.open = false
		n.berkeley.Close(n.tls.fd)
	}
	return nb, err
}
