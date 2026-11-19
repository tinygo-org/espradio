package netlink

import (
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/soypat/lneto/crypto/tlsauto"
	"github.com/soypat/lneto/x/xnet"
	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

func TestNetConnectMissingSSID(t *testing.T) {
	var e Esplink
	err := e.NetConnect(&nl.ConnectParams{})
	if err != nl.ErrMissingSSID {
		t.Errorf("NetConnect with empty SSID = %v; want ErrMissingSSID", err)
	}
}

func TestGetHostByNameEmpty(t *testing.T) {
	var e Esplink
	_, err := e.GetHostByName("")
	if err == nil {
		t.Error("GetHostByName(\"\") = nil error; want error")
	}
}

func TestGetHostByNameIPv4(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"192.168.1.1", "192.168.1.1"},
		{"10.0.0.1", "10.0.0.1"},
		{"0.0.0.0", "0.0.0.0"},
	}
	var e Esplink
	for _, tt := range tests {
		got, err := e.GetHostByName(tt.input)
		if err != nil {
			t.Errorf("GetHostByName(%q) error = %v; want nil", tt.input, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("GetHostByName(%q) = %v; want %v", tt.input, got.String(), tt.want)
		}
	}
}

func TestGetHostByNameInvalidIPv4(t *testing.T) {
	var e Esplink
	_, err := e.GetHostByName("999.999.999.999")
	if err == nil {
		t.Error("GetHostByName(\"999.999.999.999\") = nil error; want parse error")
	}
}

func TestAPParamsWithDefaultsEmpty(t *testing.T) {
	got := APConnectParams{}.withDefaults()
	if want := netip.MustParseAddr("192.168.4.1"); got.StaticAddr != want {
		t.Errorf("StaticAddr = %v; want %v", got.StaticAddr, want)
	}
	if got.Hostname != defaultHostname {
		t.Errorf("Hostname = %q; want %q", got.Hostname, defaultHostname)
	}
	if got.PassivePeers != 64 {
		t.Errorf("PassivePeers = %d; want 64", got.PassivePeers)
	}
}

func TestAPParamsWithDefaultsHostnameFromSSID(t *testing.T) {
	got := APConnectParams{
		APConfig: espradio.APConfig{SSID: "my-network"},
	}.withDefaults()
	if got.Hostname != "my-network" {
		t.Errorf("Hostname = %q; want %q", got.Hostname, "my-network")
	}
}

func TestAPParamsWithDefaultsPreservesValues(t *testing.T) {
	in := APConnectParams{
		APConfig:     espradio.APConfig{SSID: "ssid"},
		StaticAddr:   netip.MustParseAddr("10.0.0.1"),
		Hostname:     "explicit-host",
		PassivePeers: 16,
	}
	got := in.withDefaults()
	if got.StaticAddr != in.StaticAddr {
		t.Errorf("StaticAddr = %v; want %v", got.StaticAddr, in.StaticAddr)
	}
	if got.Hostname != in.Hostname {
		t.Errorf("Hostname = %q; want %q", got.Hostname, in.Hostname)
	}
	if got.PassivePeers != in.PassivePeers {
		t.Errorf("PassivePeers = %d; want %d", got.PassivePeers, in.PassivePeers)
	}
}

func TestAPParamsWithDefaultsRejectsIPv6(t *testing.T) {
	// A non-IPv4 StaticAddr must be replaced with the IPv4 default.
	got := APConnectParams{
		StaticAddr: netip.MustParseAddr("fe80::1"),
	}.withDefaults()
	if want := netip.MustParseAddr("192.168.4.1"); got.StaticAddr != want {
		t.Errorf("StaticAddr = %v; want %v", got.StaticAddr, want)
	}
}

func TestParseIPLiteral(t *testing.T) {
	tests := []struct {
		input   string
		literal bool
		wantErr bool
	}{
		{"192.168.1.1", true, false},
		{"fe80::1", true, false},
		{"999.999.999.999", true, true},
		{"9gag.com", false, false},
		{"1e100.net", false, false},
		{"example.com", false, false},
	}
	for _, tt := range tests {
		_, literal, err := parseIPLiteral(tt.input)
		if literal != tt.literal || (err != nil) != tt.wantErr {
			t.Errorf("parseIPLiteral(%q) = %v, %v; want literal %v, error %v", tt.input, literal, err, tt.literal, tt.wantErr)
		}
	}
}

func TestAPParamsWithDefaultsDHCPSubnet(t *testing.T) {
	got := APConnectParams{}.withDefaults()
	if want := netip.MustParsePrefix("192.168.4.0/24"); got.DHCPSubnet != want {
		t.Errorf("DHCPSubnet = %v; want %v", got.DHCPSubnet, want)
	}
	got = APConnectParams{StaticAddr: netip.MustParseAddr("10.0.0.1")}.withDefaults()
	if want := netip.MustParsePrefix("10.0.0.0/24"); got.DHCPSubnet != want {
		t.Errorf("DHCPSubnet = %v; want %v", got.DHCPSubnet, want)
	}
}

func TestTCPConfigApply(t *testing.T) {
	def := xnet.TCPPoolConfig{PoolSize: 4, QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024}
	if got := (TCPConfig{}).apply(def); got.PoolSize != 4 || got.QueueSize != 4 || got.TxBufSize != 4096 || got.RxBufSize != 1024 {
		t.Errorf("zero TCPConfig changed defaults: %+v", got)
	}
	got := TCPConfig{PoolSize: 1, TxBufSize: 2048}.apply(def)
	if got.PoolSize != 1 || got.QueueSize != 4 || got.TxBufSize != 2048 || got.RxBufSize != 1024 {
		t.Errorf("partial TCPConfig: %+v", got)
	}
}

func TestTLSSocketWithoutConfig(t *testing.T) {
	var e Esplink
	_, err := e.Socket(xnet.AF_INET, xnet.SOCK_STREAM, ipprotoTLS)
	if err != errNoTLSConfig {
		t.Errorf("Socket(IPPROTO_TLS) without TLS config = %v; want errNoTLSConfig", err)
	}
}

func TestTLSSocketBusy(t *testing.T) {
	var e Esplink
	UseTLS(&e, &tlsauto.Config{})
	e.berkeley = *xnet.NewBerkeleyStack(e.ns.StackGo().Socket)
	fd, err := e.Socket(xnet.AF_INET, xnet.SOCK_STREAM, ipprotoTLS)
	if err != nil {
		t.Fatal(err)
	}
	if !e.isTLS(fd) {
		t.Errorf("isTLS(%d) = false; want true", fd)
	}
	if _, err = e.Socket(xnet.AF_INET, xnet.SOCK_STREAM, ipprotoTLS); err != errTLSBusy {
		t.Errorf("second Socket(IPPROTO_TLS) = %v; want errTLSBusy", err)
	}
	e.Close(fd)
	if e.isTLS(fd) {
		t.Errorf("isTLS(%d) after Close = true; want false", fd)
	}
	if _, err = e.Socket(xnet.AF_INET, xnet.SOCK_STREAM, ipprotoTLS); err != nil {
		t.Errorf("Socket(IPPROTO_TLS) after Close = %v; want nil", err)
	}
}

type fakeTLSConn struct {
	s        *tlsSocket
	deadline time.Time
}

func (f *fakeTLSConn) Configure(tlsauto.Config) error { return nil }
func (f *fakeTLSConn) Open(io.ReadWriter) error       { return nil }
func (f *fakeTLSConn) Handshake() error               { f.deadline = f.s.deadline; return nil }
func (f *fakeTLSConn) Read(b []byte) (int, error)     { return 0, nil }
func (f *fakeTLSConn) Write(b []byte) (int, error)    { return len(b), nil }
func (f *fakeTLSConn) Close() error                   { return nil }

func TestTLSHandshakeTimeout(t *testing.T) {
	var e Esplink
	UseTLS(&e, &tlsauto.Config{})
	fake := &fakeTLSConn{s: &e.tls}
	e.tls.conn = fake
	if err := e.tlsHandshake("example.com"); err != nil {
		t.Fatal(err)
	}
	if !fake.deadline.IsZero() {
		t.Errorf("deadline without timeout = %v; want zero", fake.deadline)
	}
	e.TLSHandshakeTimeout = 5 * time.Second
	before := time.Now()
	if err := e.tlsHandshake("example.com"); err != nil {
		t.Fatal(err)
	}
	if d := fake.deadline.Sub(before); d < 5*time.Second || d > 6*time.Second {
		t.Errorf("deadline = now + %v; want about 5s", d)
	}
}
