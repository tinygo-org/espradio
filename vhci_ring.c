//go:build esp32c3 || esp32c3_qemu_target

/* VHCI RX ring buffer (controller -> host).
 *
 * Split out of bt_ble.c because this is the one part of the BLE path with no
 * dependency on the BTDM blob or on hardware: the controller pushes bytes in
 * from its recv callback and Go drains them. That makes it the only piece
 * reachable from the esp32c3-qemu unit-test target, so it lives here and
 * bt_ble.c calls espradio_vhci_ring_push() from vhci_host_recv_cb().
 */

#include <stdint.h>

#define VHCI_RX_BUF_SIZE 2048

static uint8_t s_vhci_rx_buf[VHCI_RX_BUF_SIZE];
static volatile uint32_t s_vhci_rx_head; /* written by ISR (recv callback) */
static volatile uint32_t s_vhci_rx_tail; /* read by Go */

/* Append up to len bytes, returning how many were actually stored.
 *
 * head == tail means empty, so usable capacity is VHCI_RX_BUF_SIZE - 1 and a
 * push that hits the limit stores a prefix and reports the short count. The
 * caller is an HCI packet boundary, so a short return means a truncated packet
 * was committed to the stream -- see espradio_vhci_ring_dropped(). */
int espradio_vhci_ring_push(const uint8_t *data, int len) {
    int n = 0;
    for (; n < len; n++) {
        uint32_t next = (s_vhci_rx_head + 1) % VHCI_RX_BUF_SIZE;
        if (next == s_vhci_rx_tail) {
            break; /* full */
        }
        s_vhci_rx_buf[s_vhci_rx_head] = data[n];
        s_vhci_rx_head = next;
    }
    return n;
}

int espradio_vhci_buffered(void) {
    uint32_t h = s_vhci_rx_head;
    uint32_t t = s_vhci_rx_tail;
    if (h >= t) return (int)(h - t);
    return (int)(VHCI_RX_BUF_SIZE - t + h);
}

int espradio_vhci_read_byte(void) {
    if (s_vhci_rx_head == s_vhci_rx_tail) {
        return -1; /* no data */
    }
    uint8_t b = s_vhci_rx_buf[s_vhci_rx_tail];
    s_vhci_rx_tail = (s_vhci_rx_tail + 1) % VHCI_RX_BUF_SIZE;
    return (int)b;
}

int espradio_vhci_read(uint8_t *buf, int max_len) {
    int n = 0;
    while (n < max_len && s_vhci_rx_head != s_vhci_rx_tail) {
        buf[n++] = s_vhci_rx_buf[s_vhci_rx_tail];
        s_vhci_rx_tail = (s_vhci_rx_tail + 1) % VHCI_RX_BUF_SIZE;
    }
    return n;
}

/* Test support. */
int espradio_vhci_ring_capacity(void) { return VHCI_RX_BUF_SIZE - 1; }

void espradio_vhci_ring_reset(void) {
    s_vhci_rx_head = 0;
    s_vhci_rx_tail = 0;
}
