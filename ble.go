//go:build esp32 || esp32c3 || esp32s3

package espradio

/*
#include "espradio.h"
*/
import "C"

import (
	"errors"
	"time"
	"unsafe"
)

// bleArenaPoolSize is the arena the BT controller allocates from.
//
// The 22 NULL TX buffers in the ESP32-C3 boot log are not arena exhaustion.
// The controller allocates those EM areas at the first advertise or connection.
const bleArenaPoolSize = arenaPoolSize

func (t VHCITransport) write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	n := int(C.espradio_vhci_write((*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
	return n, nil
}

func bleInit() error {
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
	if !schedStarted {
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
