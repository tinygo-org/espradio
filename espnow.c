#include "espradio.h"

static void espradio_esp_now_recv_cb(const esp_now_recv_info_t *info, const uint8_t *data, int data_len) {
    const uint8_t *src_addr = NULL;
    const uint8_t *dest_addr = NULL;
    int rssi = 0;
    uint8_t channel = 0;
    uint8_t secondary_channel = 0;
    int noise_floor = 0;
    uint32_t timestamp = 0;

    if (info != NULL) {
        src_addr = info->src_addr;
        dest_addr = info->des_addr;
        if (info->rx_ctrl != NULL) {
            rssi = info->rx_ctrl->rssi;
            channel = info->rx_ctrl->channel;
            secondary_channel = info->rx_ctrl->secondary_channel;
            noise_floor = info->rx_ctrl->noise_floor;
            timestamp = info->rx_ctrl->timestamp;
        }
    }

    espradio_on_esp_now_recv(src_addr, dest_addr, rssi, channel, secondary_channel, noise_floor, timestamp, data, data_len);
}

static void espradio_esp_now_send_cb(const esp_now_send_info_t *tx_info, esp_now_send_status_t status) {
    const uint8_t *dest_addr = NULL;
    const uint8_t *src_addr = NULL;
    wifi_interface_t ifidx = WIFI_IF_STA;
    wifi_phy_rate_t rate = 0;
    wifi_tx_status_t tx_status = WIFI_SEND_FAIL;

    if (tx_info != NULL) {
        dest_addr = tx_info->des_addr;
        src_addr = tx_info->src_addr;
        ifidx = tx_info->ifidx;
        rate = tx_info->rate;
        tx_status = tx_info->tx_status;
    }

    espradio_on_esp_now_send(dest_addr, src_addr, ifidx, rate, tx_status, status);
}

esp_err_t espradio_esp_now_register_recv_cb(void) {
    return esp_now_register_recv_cb(espradio_esp_now_recv_cb);
}

esp_err_t espradio_esp_now_register_send_cb(void) {
    return esp_now_register_send_cb(espradio_esp_now_send_cb);
}

esp_err_t espradio_esp_now_fetch_peer(int from_head, esp_now_peer_info_t *peer) {
    return esp_now_fetch_peer(from_head != 0, peer);
}

void espradio_esp_now_peer_set_encrypt(esp_now_peer_info_t *peer, int encrypt) {
    if (peer != NULL) {
        peer->encrypt = encrypt != 0;
    }
}
