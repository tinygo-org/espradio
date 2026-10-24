package espradio

import "runtime"

// VHCITransport provides HCI read/write access to the BLE controller
// via the Virtual HCI interface. It implements the transport interface
// expected by the tinygo bluetooth package.
type VHCITransport struct{}

// Buffered returns the number of bytes available to read from the controller.
func (t VHCITransport) Buffered() int {
	return vhciBuffered()
}

// ReadByte reads a single byte from the HCI controller.
func (t VHCITransport) ReadByte() (byte, error) {
	for {
		b := vhciReadByte()
		if b >= 0 {
			return byte(b), nil
		}
		runtime.Gosched()
	}
}

// Read reads up to len(buf) bytes from the HCI controller.
func (t VHCITransport) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	for {
		n := vhciRead(buf)
		if n > 0 {
			return n, nil
		}
		runtime.Gosched()
	}
}

// Write sends an HCI packet to the BLE controller.
func (t VHCITransport) Write(buf []byte) (v int, err error) {
	onCore0(func() { v, err = t.write(buf) })
	return
}

// BLEInit initializes the BLE controller.
// Must be called before using VHCITransport.
func BLEInit() (err error) {
	onCore0(func() { err = bleInit() })
	return
}
