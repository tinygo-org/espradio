package cesp

// NetifStartRx initializes the C-side RX ring and registers the WiFi receive
// callback. apMode=1 selects the AP interface, 0 selects STA.
func NetifStartRx(apMode int) error { return netifStartRx(apMode) }

// NetifTx transmits buf as a raw Ethernet frame on the WiFi interface.
func NetifTx(buf []byte) error { return netifTx(buf) }

// NetifRxAvailable reports whether at least one received frame is waiting.
func NetifRxAvailable() bool { return netifRxAvailable() }

// NetifRxPop copies the next received frame into dst and returns its length.
func NetifRxPop(dst []byte) int { return netifRxPop(dst) }

// NetifGetMAC returns the 6-byte MAC address of the WiFi interface.
func NetifGetMAC() ([6]byte, error) { return netifGetMAC() }

// NetifRxStats returns (callback_count, drop_count) from the C RX ring.
func NetifRxStats() (uint32, uint32) { return netifRxStats() }

// NetifSetConnected updates the C-side connection state.
func NetifSetConnected(connected bool) { netifSetConnected(connected) }

// NetifInitNetstackCB installs the netstack WiFi RX/TX callbacks.
func NetifInitNetstackCB() { netifInitNetstackCB() }
