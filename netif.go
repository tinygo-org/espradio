//go:build esp32c3

package espradio

/*
#include "include.h"
#include <string.h>

extern esp_err_t esp_wifi_internal_reg_rxcb(wifi_interface_t ifx, esp_err_t (*fn)(void *, uint16_t, void *));
extern int esp_wifi_internal_tx(wifi_interface_t wifi_if, void *buffer, uint16_t len);
extern void esp_wifi_internal_free_rx_buffer(void *buffer);
extern esp_err_t esp_wifi_get_mac(wifi_interface_t ifx, uint8_t mac[6]);

// Ring buffer for received frames.
// The RX callback (called from the WiFi task goroutine) copies each frame here
// and a Go goroutine delivers them to the consumer.

#define ESPRADIO_NETIF_RXRING_SIZE  8
#define ESPRADIO_NETIF_FRAME_MAX   1600

typedef struct {
	uint8_t  data[ESPRADIO_NETIF_FRAME_MAX];
	uint16_t len;
} espradio_rx_frame_t;

static espradio_rx_frame_t s_rx_ring[ESPRADIO_NETIF_RXRING_SIZE];
static volatile uint32_t   s_rx_head;
static volatile uint32_t   s_rx_tail;

static esp_err_t espradio_sta_rxcb(void *buffer, uint16_t len, void *eb) {
	uint32_t next = (s_rx_head + 1) % ESPRADIO_NETIF_RXRING_SIZE;
	if (next == s_rx_tail) {
		// ring full — drop frame
		esp_wifi_internal_free_rx_buffer(eb);
		return 0;
	}
	uint16_t copy_len = len;
	if (copy_len > ESPRADIO_NETIF_FRAME_MAX) copy_len = ESPRADIO_NETIF_FRAME_MAX;
	memcpy(s_rx_ring[s_rx_head].data, buffer, copy_len);
	s_rx_ring[s_rx_head].len = copy_len;
	s_rx_head = next;
	esp_wifi_internal_free_rx_buffer(eb);
	return 0;
}

static esp_err_t espradio_netif_start_rx(void) {
	s_rx_head = 0;
	s_rx_tail = 0;
	return esp_wifi_internal_reg_rxcb(WIFI_IF_STA, espradio_sta_rxcb);
}

static int espradio_netif_rx_available(void) {
	return s_rx_head != s_rx_tail;
}

static uint16_t espradio_netif_rx_pop(void *dst, uint16_t dst_len) {
	if (s_rx_head == s_rx_tail) return 0;
	uint16_t len = s_rx_ring[s_rx_tail].len;
	if (len > dst_len) len = dst_len;
	memcpy(dst, s_rx_ring[s_rx_tail].data, len);
	s_rx_tail = (s_rx_tail + 1) % ESPRADIO_NETIF_RXRING_SIZE;
	return len;
}

static int espradio_netif_tx(void *buf, uint16_t len) {
	return esp_wifi_internal_tx(WIFI_IF_STA, buf, len);
}

static esp_err_t espradio_netif_get_mac(uint8_t mac[6]) {
	return esp_wifi_get_mac(WIFI_IF_STA, mac);
}
*/
import "C"

import (
	"time"
	"unsafe"
)

var _ L2Device = (*NetDev)(nil)

// NetDev provides raw Ethernet frame I/O over the WiFi STA interface.
type NetDev struct {
	rxCh chan []byte
	done chan struct{}
}

// StartNetDev registers the RX callback and starts the receive pump.
// Call after Connect() succeeds.
func StartNetDev() (*NetDev, error) {
	if code := C.espradio_netif_start_rx(); code != C.ESP_OK {
		return nil, makeError(code)
	}
	nd := &NetDev{
		rxCh: make(chan []byte, 8),
		done: make(chan struct{}),
	}
	go nd.rxPump()
	return nd, nil
}

func (nd *NetDev) rxPump() {
	buf := make([]byte, 1600)
	for {
		select {
		case <-nd.done:
			return
		default:
		}
		if C.espradio_netif_rx_available() == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		n := C.espradio_netif_rx_pop(unsafe.Pointer(&buf[0]), C.uint16_t(len(buf)))
		if n == 0 {
			continue
		}
		frame := make([]byte, int(n))
		copy(frame, buf[:n])
		select {
		case nd.rxCh <- frame:
		default:
			// consumer too slow — drop
		}
	}
}

// RecvEth blocks until an Ethernet frame is received. Returns the frame data.
func (nd *NetDev) RecvEth() ([]byte, error) {
	frame, ok := <-nd.rxCh
	if !ok {
		return nil, Error(C.ESP_ERR_INVALID_STATE)
	}
	return frame, nil
}

// RecvCh returns a channel that delivers received Ethernet frames.
func (nd *NetDev) RecvCh() <-chan []byte {
	return nd.rxCh
}

// SendEth transmits a raw Ethernet frame through the WiFi STA interface.
func (nd *NetDev) SendEth(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	code := C.espradio_netif_tx(unsafe.Pointer(&frame[0]), C.uint16_t(len(frame)))
	if code != 0 {
		return makeError(C.esp_err_t(code))
	}
	return nil
}

// HardwareAddr returns the 6-byte MAC address of the STA interface.
func (nd *NetDev) HardwareAddr() ([6]byte, error) {
	var mac [6]byte
	code := C.espradio_netif_get_mac((*C.uint8_t)(unsafe.Pointer(&mac[0])))
	if code != C.ESP_OK {
		return mac, makeError(code)
	}
	return mac, nil
}

// MTU returns the Maximum Transmission Unit for the Ethernet interface.
func (nd *NetDev) MTU() int {
	return EthMTU
}

// Close stops the receive pump.
func (nd *NetDev) Close() {
	select {
	case <-nd.done:
	default:
		close(nd.done)
	}
}
