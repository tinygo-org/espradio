//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package espradio

import (
	"github.com/soypat/lneto/x/netdev"
	"tinygo.org/x/espradio/cesp"
)

var _ netdev.DevEthernet = (*Handle)(nil)

// var _ netdev.Interface[STAConfig] = (*Handle)(nil)

// Handle is a raw netdev.DevEthernet implementation over the ESP WiFi interface.
// Call Start before use.
type Handle struct {
	rxHandler func([]byte)
}

func (h *Handle) LinkConnect(cfg STAConfig) error {
	return cesp.WifiSetSTAConfig(cfg.SSID, cfg.Password)
}

// Start initializes the C-side RX ring and registers the WiFi receive callback.
// apMode selects the AP interface when true, STA otherwise.
func (h *Handle) Start(apMode bool) error {
	ap := 0
	if apMode {
		ap = 1
	}
	return cesp.NetifStartRx(ap)
}

func (h *Handle) HardwareAddr6() ([6]byte, error) {
	return cesp.NetifGetMAC()
}

// SendOffsetEthFrame transmits a raw Ethernet frame. frameOff is 0 for this
// device so the frame starts at buf[0].
func (h *Handle) SendOffsetEthFrame(buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	return cesp.NetifTx(buf)
}

func (h *Handle) SetEthRecvHandler(handler func(rxEthframe []byte)) {
	h.rxHandler = handler
}

func (h *Handle) EthPoll(buf []byte) (ethFrameOff, ethernetBytes int, err error) {
	if !cesp.NetifRxAvailable() {
		return 0, 0, nil
	}
	n := cesp.NetifRxPop(buf)
	if n == 0 {
		return 0, 0, nil
	}
	if h.rxHandler != nil {
		h.rxHandler(buf[:n])
	}
	return 0, n, nil
}

// MaxFrameSizeAndOffset returns the maximum frame size and a zero offset,
// meaning the Ethernet frame begins at byte 0 of every buffer.
func (h *Handle) MaxFrameSizeAndOffset() (maxFrameSize int, frameOff int) {
	return MaxFrameSize, 0
}
