//go:build esp32c3 || esp32s3

package espradio

/*
#include "espradio.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"time"
	"unsafe"
)

// bleArenaPoolSize is the arena the BT controller allocates from.
//
// The 22 NULL TX buffers in the ESP32-C3 boot log are not arena exhaustion.
// The controller allocates those EM areas at the first advertise or connection.
const bleArenaPoolSize = arenaPoolSize

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
func (t VHCITransport) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	n := int(C.espradio_vhci_write((*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
	return n, nil
}

// BLEInit initializes the BLE controller.
// Must be called before using VHCITransport.
func BLEInit() error {
	// Ensure radio hardware is powered up (clocks, power domain, modem reset).
	// This is normally done by Enable() for WiFi, but BLE may be used standalone.
	if err := initHardware(); err != nil {
		return err
	}

	// Ensure arena allocator is initialized. The BT blob needs malloc for
	// internal function tables. If WiFi Enable() was already called, this
	// is a no-op (arenaPool is already set).
	if arenaPool == nil {
		arenaPool = makeArenaPool(bleArenaPoolSize)
		C.espradio_arena_init((*C.uint8_t)(unsafe.Pointer(&arenaPool[0])), C.size_t(len(arenaPool)))
	}

	// Start scheduler ticker if not already running (provides periodic
	// execution for the BLE controller task goroutine via kickSched).
	if isrKick == nil {
		startSchedTicker()
	}

	// Wire BT interrupts before controller init
	setupBTInterrupts()

	// Initialize the BLE controller (C side):
	//   bt_clock → rom_data_init → osi_register → controller_init → phy_enable → controller_enable → vhci
	res := int(C.espradio_ble_init())
	if res != 0 {
		return errors.New("espradio: BLE init failed")
	}

	// Enable hardware interrupts for BT
	C.espradio_bt_enable_hw_interrupts()

	// Give the controller task time to stabilize
	time.Sleep(100 * time.Millisecond)

	return nil
}
