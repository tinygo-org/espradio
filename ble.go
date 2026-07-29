//go:build esp32c3

package espradio

/*
#include "espradio.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"runtime/interrupt"
	"time"
	"unsafe"
)

// bleArenaPoolSize is the arena the BT controller allocates from.
//
// Note: the boot log shows the 10 ADV DATA TX buffers and 12 ACL TX buffers
// coming back NULL with their EM_BASE_REG LUT entries set to 0x3fc00000, and the
// DB heap getting 0 bytes. That looks like arena exhaustion but is not — raising
// this to 96 KB reproduced the identical 22 NULLs, so those EM regions are
// allocated lazily (on first advertise / first connection) and the 0x3fc00000
// bases are simply "unmapped". Left at the WiFi size.
const bleArenaPoolSize = arenaPoolSize

// VHCITransport provides HCI read/write access to the ESP32 BLE controller
// via the Virtual HCI interface. It implements the transport interface
// expected by the tinygo bluetooth package.
type VHCITransport struct{}

// Buffered returns the number of bytes available to read from the controller.
func (t VHCITransport) Buffered() int {
	return int(C.espradio_vhci_buffered())
}

// ReadByte reads a single byte from the HCI controller.
func (t VHCITransport) ReadByte() (byte, error) {
	for {
		b := int(C.espradio_vhci_read_byte())
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
		n := int(C.espradio_vhci_read((*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
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

// BLEInit initializes the BLE controller on the ESP32-C3.
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

// ─── BT Interrupt Handlers ───────────────────────────────────────────────────

// CPU interrupt numbers for BT (must match esp32c3/isr.c)
const (
	btCPUInt5 = 28 // RWBT + BT_BB → CPU interrupt 28
	btCPUInt8 = 30 // RWBLE → CPU interrupt 30
)

func setupBTInterrupts() {
	// Register and enable Go interrupt handlers (like WiFi does).
	// Enable() sets priority=5 and edge-triggered, then
	// espradio_bt_enable_hw_interrupts() overrides to priority=7 + level-triggered.
	intr5 := interrupt.New(btCPUInt5, btISR5Handler)
	intr5.Enable()
	intr8 := interrupt.New(btCPUInt8, btISR8Handler)
	intr8.Enable()
}

// Both dispatchers run blob code in real interrupt context, so they mark it:
// anything they reach that would otherwise spin-and-yield must spin instead.
func btISR5Handler(interrupt.Interrupt) {
	C.espradio_enter_hw_isr()
	C.espradio_bt_isr_dispatch_5()
	C.espradio_exit_hw_isr()
}

func btISR8Handler(interrupt.Interrupt) {
	C.espradio_enter_hw_isr()
	C.espradio_bt_isr_dispatch_8()
	C.espradio_exit_hw_isr()
}
