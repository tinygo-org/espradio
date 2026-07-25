package netlink

import (
	"net/netip"
	"testing"

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
