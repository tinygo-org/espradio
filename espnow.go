//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -fno-short-enums

#include "espradio.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

const (
	ESPNowAddressLength = C.ESP_NOW_ETH_ALEN
	ESPNowKeyLength     = C.ESP_NOW_KEY_LEN
	ESPNowMaxDataLength = C.ESP_NOW_MAX_DATA_LEN
)

type WiFiInterface uint8

const (
	WiFiInterfaceSTA WiFiInterface = C.WIFI_IF_STA
	WiFiInterfaceAP  WiFiInterface = C.WIFI_IF_AP
)

type ESPNowSendStatus uint8

const (
	ESPNowSendSuccess ESPNowSendStatus = C.ESP_NOW_SEND_SUCCESS
	ESPNowSendFail    ESPNowSendStatus = C.ESP_NOW_SEND_FAIL
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

var (
	espNowMu            sync.RWMutex
	espNowRecvHandler   func(ESPNowReceive)
	espNowSendHandler   func(ESPNowSendReport)
	activeManagedESPNow *ESPNow
)

// ESPNowInit initializes the ESP-NOW subsystem and registers callback trampolines.
func ESPNowInit() (err error) {
	onCore0(func() { err = espNowInit() })
	return
}

func espNowInit() error {
	if code := C.esp_now_init(); code != C.ESP_OK {
		return makeError(code)
	}
	if code := C.espradio_esp_now_register_recv_cb(); code != C.ESP_OK {
		_ = C.esp_now_deinit()
		return makeError(code)
	}
	if code := C.espradio_esp_now_register_send_cb(); code != C.ESP_OK {
		_ = C.esp_now_unregister_recv_cb()
		_ = C.esp_now_deinit()
		return makeError(code)
	}
	return nil
}

// ESPNowDeinit deinitializes ESP-NOW and unregisters any callback trampolines.
func ESPNowDeinit() (err error) {
	onCore0(func() { err = espNowDeinit() })
	return
}

func espNowDeinit() error {
	if code := C.esp_now_unregister_recv_cb(); code != C.ESP_OK {
		return makeError(code)
	}
	if code := C.esp_now_unregister_send_cb(); code != C.ESP_OK {
		return makeError(code)
	}
	if code := C.esp_now_deinit(); code != C.ESP_OK {
		return makeError(code)
	}
	return nil
}

// ESPNowVersion returns the underlying ESP-NOW version reported by the SDK.
func ESPNowVersion() (v uint32, err error) {
	onCore0(func() { v, err = espNowVersion() })
	return
}

func espNowVersion() (uint32, error) {
	var version C.uint32_t
	if code := C.esp_now_get_version(&version); code != C.ESP_OK {
		return 0, makeError(code)
	}
	return uint32(version), nil
}

// ESPNowSetPrimaryMasterKey configures the 16-byte PMK used to encrypt LMKs.
func ESPNowSetPrimaryMasterKey(key [ESPNowKeyLength]byte) (err error) {
	onCore0(func() { err = espNowSetPrimaryMasterKey(key) })
	return
}

func espNowSetPrimaryMasterKey(key [ESPNowKeyLength]byte) error {
	return makeError(C.esp_now_set_pmk((*C.uint8_t)(unsafe.Pointer(&key[0]))))
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

// espNowSendOnCore0 keeps the closure out of ESPNowSend, since its captured values are allocated.
func espNowSendOnCore0(peer *[ESPNowAddressLength]byte, data []byte) (err error) {
	onCore0(func() { err = espNowSend(peer, data) })
	return
}

func espNowSend(peer *[ESPNowAddressLength]byte, data []byte) error {
	var peerPtr *C.uint8_t
	if peer != nil {
		peerPtr = (*C.uint8_t)(unsafe.Pointer(&peer[0]))
	}
	var dataPtr *C.uint8_t
	if len(data) > 0 {
		dataPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	return makeError(C.esp_now_send(peerPtr, dataPtr, C.size_t(len(data))))
}

// ESPNowAddPeer adds a peer to the SDK-maintained peer table.
func ESPNowAddPeer(peer ESPNowPeer) (err error) {
	onCore0(func() { err = espNowAddPeer(peer) })
	return
}

func espNowAddPeer(peer ESPNowPeer) error {
	cpeer := cESPNowPeer(peer)
	return makeError(C.esp_now_add_peer(&cpeer))
}

// ESPNowDeletePeer removes a peer from the SDK-maintained peer table.
func ESPNowDeletePeer(addr [ESPNowAddressLength]byte) (err error) {
	onCore0(func() { err = espNowDeletePeer(addr) })
	return
}

func espNowDeletePeer(addr [ESPNowAddressLength]byte) error {
	return makeError(C.esp_now_del_peer((*C.uint8_t)(unsafe.Pointer(&addr[0]))))
}

// ESPNowModifyPeer updates an existing peer record.
func ESPNowModifyPeer(peer ESPNowPeer) (err error) {
	onCore0(func() { err = espNowModifyPeer(peer) })
	return
}

func espNowModifyPeer(peer ESPNowPeer) error {
	cpeer := cESPNowPeer(peer)
	return makeError(C.esp_now_mod_peer(&cpeer))
}

// ESPNowGetPeer looks up a peer by MAC address.
func ESPNowGetPeer(addr [ESPNowAddressLength]byte) (v ESPNowPeer, err error) {
	onCore0(func() { v, err = espNowGetPeer(addr) })
	return
}

func espNowGetPeer(addr [ESPNowAddressLength]byte) (ESPNowPeer, error) {
	var cpeer C.esp_now_peer_info_t
	if code := C.esp_now_get_peer((*C.uint8_t)(unsafe.Pointer(&addr[0])), &cpeer); code != C.ESP_OK {
		return ESPNowPeer{}, makeError(code)
	}
	return goESPNowPeer(cpeer), nil
}

// ESPNowFetchPeer fetches the next peer from the peer table.
func ESPNowFetchPeer(fromHead bool) (v ESPNowPeer, err error) {
	onCore0(func() { v, err = espNowFetchPeer(fromHead) })
	return
}

func espNowFetchPeer(fromHead bool) (ESPNowPeer, error) {
	var cpeer C.esp_now_peer_info_t
	if code := C.espradio_esp_now_fetch_peer(C.int(boolToInt(fromHead)), &cpeer); code != C.ESP_OK {
		return ESPNowPeer{}, makeError(code)
	}
	return goESPNowPeer(cpeer), nil
}

// ESPNowPeerExists reports whether a peer exists in the peer table.
func ESPNowPeerExists(addr [ESPNowAddressLength]byte) (v bool) {
	onCore0(func() { v = espNowPeerExists(addr) })
	return
}

func espNowPeerExists(addr [ESPNowAddressLength]byte) bool {
	return bool(C.esp_now_is_peer_exist((*C.uint8_t)(unsafe.Pointer(&addr[0]))))
}

// ESPNowGetPeerCount returns the total and encrypted peer counts.
func ESPNowGetPeerCount() (v ESPNowPeerCount, err error) {
	onCore0(func() { v, err = espNowGetPeerCount() })
	return
}

func espNowGetPeerCount() (ESPNowPeerCount, error) {
	var counts C.esp_now_peer_num_t
	if code := C.esp_now_get_peer_num(&counts); code != C.ESP_OK {
		return ESPNowPeerCount{}, makeError(code)
	}
	return ESPNowPeerCount{
		Total:     int(counts.total_num),
		Encrypted: int(counts.encrypt_num),
	}, nil
}

func cESPNowPeer(peer ESPNowPeer) C.esp_now_peer_info_t {
	var cpeer C.esp_now_peer_info_t
	copy(cArrayToBytes((*C.uint8_t)(unsafe.Pointer(&cpeer.peer_addr[0])), ESPNowAddressLength), peer.Address[:])
	copy(cArrayToBytes((*C.uint8_t)(unsafe.Pointer(&cpeer.lmk[0])), ESPNowKeyLength), peer.Key[:])
	cpeer.channel = C.uint8_t(peer.Channel)
	cpeer.ifidx = C.wifi_interface_t(peer.If)
	C.espradio_esp_now_peer_set_encrypt(&cpeer, C.int(boolToInt(peer.Encrypt)))
	cpeer.priv = nil
	return cpeer
}

func goESPNowPeer(peer C.esp_now_peer_info_t) ESPNowPeer {
	var out ESPNowPeer
	copy(out.Address[:], cArrayToBytes((*C.uint8_t)(unsafe.Pointer(&peer.peer_addr[0])), ESPNowAddressLength))
	copy(out.Key[:], cArrayToBytes((*C.uint8_t)(unsafe.Pointer(&peer.lmk[0])), ESPNowKeyLength))
	out.Channel = uint8(peer.channel)
	out.If = WiFiInterface(peer.ifidx)
	out.Encrypt = bool(peer.encrypt)
	return out
}

func cArrayToBytes(ptr *C.uint8_t, n int) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n)
}

func copyMAC(ptr *C.uint8_t) [ESPNowAddressLength]byte {
	var out [ESPNowAddressLength]byte
	if ptr != nil {
		copy(out[:], cArrayToBytes(ptr, ESPNowAddressLength))
	}
	return out
}

//export espradio_on_esp_now_recv
func espradio_on_esp_now_recv(srcAddr, destAddr *C.uint8_t, rssi C.int, channel, secondaryChannel C.uint8_t, noiseFloor C.int, timestamp C.uint32_t, data *C.uint8_t, dataLen C.int) {
	espNowMu.RLock()
	handler := espNowRecvHandler
	manager := activeManagedESPNow
	espNowMu.RUnlock()
	if handler == nil && manager == nil {
		return
	}

	event := ESPNowReceive{
		SourceAddress:      copyMAC(srcAddr),
		DestinationAddress: copyMAC(destAddr),
		RSSI:               int8(rssi),
		Channel:            uint8(channel),
		SecondaryChannel:   uint8(secondaryChannel),
		NoiseFloor:         int8(noiseFloor),
		Timestamp:          uint32(timestamp),
	}
	if data != nil && dataLen > 0 {
		event.Data = unsafe.Slice((*byte)(unsafe.Pointer(data)), int(dataLen))
	}
	if handler != nil {
		handler(event)
	}
	if manager != nil {
		manager.handleReceive(event)
	}
}

//export espradio_on_esp_now_send
func espradio_on_esp_now_send(destAddr, srcAddr *C.uint8_t, ifidx C.wifi_interface_t, rate C.wifi_phy_rate_t, txStatus C.wifi_tx_status_t, status C.esp_now_send_status_t) {
	espNowMu.RLock()
	handler := espNowSendHandler
	manager := activeManagedESPNow
	espNowMu.RUnlock()
	if handler == nil && manager == nil {
		return
	}

	report := ESPNowSendReport{
		DestinationAddress: copyMAC(destAddr),
		SourceAddress:      copyMAC(srcAddr),
		If:                 WiFiInterface(ifidx),
		Rate:               uint32(rate),
		TxStatus:           ESPNowSendStatus(txStatus),
		Status:             ESPNowSendStatus(status),
	}
	if handler != nil {
		handler(report)
	}
	if manager != nil {
		manager.handleSend(report)
	}
}
