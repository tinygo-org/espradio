package espradio

const (
	ESPNowAddressLength = 6
	ESPNowKeyLength     = 16
	ESPNowMaxDataLength = 250
)

type WiFiInterface uint8

const (
	WiFiInterfaceSTA WiFiInterface = 0
	WiFiInterfaceAP  WiFiInterface = 1
)

type ESPNowSendStatus uint8

const (
	ESPNowSendSuccess ESPNowSendStatus = 0
	ESPNowSendFail    ESPNowSendStatus = 1
)

type ESPNowPeer struct {
	Address [ESPNowAddressLength]byte
	Key     [ESPNowKeyLength]byte
	Channel uint8
	If      WiFiInterface
	Encrypt bool
}

type ESPNowPeerCount struct {
	Total     int
	Encrypted int
}

type ESPNowReceive struct {
	SourceAddress      [ESPNowAddressLength]byte
	DestinationAddress [ESPNowAddressLength]byte
	RSSI               int8
	Channel            uint8
	SecondaryChannel   uint8
	NoiseFloor         int8
	Timestamp          uint32
	// Data points into the radio's buffer and is only valid during the handler call.
	Data []byte
}

type ESPNowSendReport struct {
	DestinationAddress [ESPNowAddressLength]byte
	SourceAddress      [ESPNowAddressLength]byte
	If                 WiFiInterface
	Rate               uint32
	TxStatus           ESPNowSendStatus
	Status             ESPNowSendStatus
}

// ESPNowInit initializes the ESP-NOW subsystem and registers callback trampolines.
func ESPNowInit() (err error) {
	onCore0(func() { err = espNowInit() })
	return
}

// ESPNowDeinit deinitializes ESP-NOW and unregisters any callback trampolines.
func ESPNowDeinit() (err error) {
	onCore0(func() { err = espNowDeinit() })
	return
}

// ESPNowVersion returns the underlying ESP-NOW version reported by the SDK.
func ESPNowVersion() (v uint32, err error) {
	onCore0(func() { v, err = espNowVersion() })
	return
}

// ESPNowSetPrimaryMasterKey configures the 16-byte PMK used to encrypt LMKs.
func ESPNowSetPrimaryMasterKey(key [ESPNowKeyLength]byte) (err error) {
	onCore0(func() { err = espNowSetPrimaryMasterKey(key) })
	return
}

// ESPNowSetReceiveHandler installs the Go callback for incoming ESP-NOW packets.
// The handler must copy Data to keep it, so receiving does not allocate.
func ESPNowSetReceiveHandler(handler func(ESPNowReceive)) {
	espNowMu.Lock()
	espNowRecvHandler = handler
	espNowMu.Unlock()
}

// ESPNowSetSendHandler installs the Go callback for ESP-NOW send completion reports.
func ESPNowSetSendHandler(handler func(ESPNowSendReport)) {
	espNowMu.Lock()
	espNowSendHandler = handler
	espNowMu.Unlock()
}

// ESPNowSend sends a packet to one peer, or to all peers when peer is nil.
// From a goroutine pinned with LockToCore0 it does not allocate.
func ESPNowSend(peer *[ESPNowAddressLength]byte, data []byte) error {
	if onCore0Direct() {
		return espNowSend(peer, data)
	}
	return espNowSendOnCore0(peer, data)
}

// ESPNowAddPeer adds a peer to the SDK-maintained peer table.
func ESPNowAddPeer(peer ESPNowPeer) (err error) {
	onCore0(func() { err = espNowAddPeer(peer) })
	return
}

// ESPNowDeletePeer removes a peer from the SDK-maintained peer table.
func ESPNowDeletePeer(addr [ESPNowAddressLength]byte) (err error) {
	onCore0(func() { err = espNowDeletePeer(addr) })
	return
}

// ESPNowModifyPeer updates an existing peer record.
func ESPNowModifyPeer(peer ESPNowPeer) (err error) {
	onCore0(func() { err = espNowModifyPeer(peer) })
	return
}

// ESPNowGetPeer looks up a peer by MAC address.
func ESPNowGetPeer(addr [ESPNowAddressLength]byte) (v ESPNowPeer, err error) {
	onCore0(func() { v, err = espNowGetPeer(addr) })
	return
}

// ESPNowFetchPeer fetches the next peer from the peer table.
func ESPNowFetchPeer(fromHead bool) (v ESPNowPeer, err error) {
	onCore0(func() { v, err = espNowFetchPeer(fromHead) })
	return
}

// ESPNowPeerExists reports whether a peer exists in the peer table.
func ESPNowPeerExists(addr [ESPNowAddressLength]byte) (v bool) {
	onCore0(func() { v = espNowPeerExists(addr) })
	return
}

// ESPNowGetPeerCount returns the total and encrypted peer counts.
func ESPNowGetPeerCount() (v ESPNowPeerCount, err error) {
	onCore0(func() { v, err = espNowGetPeerCount() })
	return
}
