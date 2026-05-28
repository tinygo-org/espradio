package cesp

import "unsafe"

// IntsOff masks the CPU interrupt given by bit position in mask.
func IntsOff(mask uint32) { isrIntsOff(mask) }

// WifiUnmask re-enables the WiFi CPU interrupt after IntsOff.
func WifiUnmask() { isrWifiUnmask() }

// CallWifiISR invokes the WiFi blob's ISR handler from goroutine context.
func CallWifiISR() { isrCallWifiISR() }

// IsrRingTail returns the current tail index of the ISR event ring.
func IsrRingTail() uint32 { return isrRingTail() }

// IsrRingHead returns the current head index of the ISR event ring.
func IsrRingHead() uint32 { return isrRingHead() }

// IsrRingEntryQueue returns the queue pointer stored at ring index idx.
func IsrRingEntryQueue(idx uint32) unsafe.Pointer { return isrRingEntryQueue(idx) }

// IsrRingEntryItem returns the item pointer stored at ring index idx.
func IsrRingEntryItem(idx uint32) unsafe.Pointer { return isrRingEntryItem(idx) }

// IsrRingAdvanceTail consumes the current tail entry.
func IsrRingAdvanceTail() { isrRingAdvanceTail() }

// IsrRingDrops returns the number of ISR events dropped due to ring overflow.
func IsrRingDrops() uint32 { return isrRingDrops() }

// GetWifiISRCount returns the total number of WiFi ISR invocations.
func GetWifiISRCount() uint32 { return isrGetWifiISRCount() }

// WifiIntRaisePriority raises the WiFi interrupt to its operational priority.
func WifiIntRaisePriority() { isrWifiIntRaisePriority() }

// PrewireWifiInterrupts sets up the WiFi interrupt vector before init.
func PrewireWifiInterrupts() { isrPrewireWifiInterrupts() }

// WifiIntToLevel switches the WiFi interrupt to level-triggered mode.
func WifiIntToLevel() { isrWifiIntToLevel() }
