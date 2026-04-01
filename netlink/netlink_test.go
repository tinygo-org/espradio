package netlink

import (
	"testing"

	nl "tinygo.org/x/drivers/netlink"
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
