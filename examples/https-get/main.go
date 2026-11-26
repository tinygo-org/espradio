// This example sends an HTTP request over crypto/tls.Dial using the tlsauto client in Esplink.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/https-get
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	_ "embed"
	"errors"
	"io"
	"runtime"
	"time"

	"github.com/soypat/lcrypto"
	"github.com/soypat/lcrypto/x25519"
	"github.com/soypat/lcrypto/x509"
	"github.com/soypat/lneto/crypto/tlsauto"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio/hwcrypto"
	"tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
	host     = "tinygo.org"
)

// ISRG Root X2 from https://letsencrypt.org/certificates/
//
//go:embed isrg-root-x2.der
var isrgRootX2 []byte

// GTS Root R4 from https://pki.goog/repository/
//
//go:embed gts-root-r4.der
var gtsRootR4 []byte

var (
	verifier x509.Verifier
	p256     x509.P256Verifier
	p384     x509.P384Verifier
	schemes  [8]uint16
	buf      [1024]byte
)

func main() {
	time.Sleep(2 * time.Second)

	err := verifier.Configure(x509.VerifierConfig{
		Roots:      x509.Chain{isrgRootX2, gtsRootR4},
		Nanotime:   func() int64 { return time.Now().UnixNano() },
		Algorithms: []x509.SignatureVerifier{&p256, &p384},
	})
	if err != nil {
		failure("verifier: " + err.Error())
	}

	link := netlink.Esplink{
		// A receive buffer that holds a whole TLS record lets tlsauto decrypt it in place, RFC 8446 5.1.
		TCP:                 netlink.TCPConfig{PoolSize: 1, RxBufSize: 18 << 10},
		TLSHandshakeTimeout: 30 * time.Second,
	}
	netlink.UseTLS(&link, &tlsauto.Config{
		Rand:     rand.Reader,
		Verifier: peerVerifier{&verifier},
		Schemes:  verifier.Schemes(schemes[:0]),
		Suites:   []tlsauto.Suite{hwcrypto.TLSAES128GCMSHA256()},
		KeyExchanges: []tlsauto.KeyExchange{{
			ID:             x25519.GroupID,
			ClientShareLen: x25519.ShareSize,
			ServerShareLen: x25519.ShareSize,
			SharedLen:      x25519.SharedSize,
			NewExchanger:   func() tlsauto.Exchanger { return new(x25519.Exchanger) },
		}},
	})
	netdev.UseNetdev(&link)

	println("Connecting to WiFi...")
	err = link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		failure("WiFi connect failed: " + err.Error())
	}

	// Certificate dates are checked, so the clock must be set first.
	offset, err := link.NTP("pool.ntp.org", 5*time.Second, 3)
	if err != nil {
		failure("NTP failed: " + err.Error())
	}
	runtime.AdjustTimeOffset(int64(offset))
	println("time is", time.Now().String())

	for cnt := 1; ; cnt++ {
		println("Getting https://" + host + "/")
		start := time.Now()
		conn, err := tls.Dial("tcp", host+":443", nil)
		if err != nil {
			println("dial failed:", err.Error())
			time.Sleep(10 * time.Second)
			continue
		}
		_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"))
		total := 0
		for err == nil {
			var n int
			n, err = conn.Read(buf[:])
			if total == 0 && n > 0 {
				status, _, _ := bytes.Cut(buf[:n], []byte("\r\n"))
				println(string(status))
			}
			total += n
		}
		conn.Close()
		switch {
		case err == io.EOF:
		case errors.Is(err, io.ErrUnexpectedEOF):
			println("server closed the connection without TLS close_notify")
		default:
			println("failed:", err.Error())
		}
		println("got", total, "bytes in", time.Since(start).String())
		println("--------", cnt, "--------")
		time.Sleep(10 * time.Second)
	}
}

func failure(msg string) {
	for {
		println("stopped after failure:", msg)
		time.Sleep(time.Second)
	}
}

// peerVerifier passes the tlsauto chain to the lcrypto verifier, which takes its own CertChain type.
type peerVerifier struct{ *x509.Verifier }

func (v peerVerifier) VerifyPeer(c tlsauto.CertChain, scheme uint16, peerIsServer bool, name, msg, sig []byte) error {
	return v.Verifier.VerifyPeer(lcrypto.CertChain(c), scheme, peerIsServer, name, msg, sig)
}
