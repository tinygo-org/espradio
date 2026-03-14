//go:build esp32c3

#include "blobs/include/esp_phy_init.h"
#include "blobs/include/esp_err.h"

#ifndef ESPRADIO_PHY_INIT_DATA_DEBUG
#define ESPRADIO_PHY_INIT_DATA_DEBUG 0
#endif

__attribute__((weak)) esp_err_t esp_phy_load_cal_data_from_nvs(esp_phy_calibration_data_t *out_cal_data) {
    (void)out_cal_data;
    return ESP_ERR_NOT_FOUND;
}

__attribute__((weak)) esp_err_t esp_phy_store_cal_data_to_nvs(const esp_phy_calibration_data_t *cal_data) {
    (void)cal_data;
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_deep_sleep_register_phy_hook(void (*hook)(void)) {
    (void)hook;
    return ESP_OK;
}

/* Скопировано из ESP-IDF: components/esp_phy/esp32c3/include/phy_init_data.h */
static const esp_phy_init_data_t phy_init_data = {{
    0x00, 0x00, 0x50, 0x50, 0x50, 0x4c, 0x4c, 0x48,
    0x4c, 0x48, 0x48, 0x44, 0x4a, 0x46, 0x46, 0x42,
    0x00, 0x00, 0x00,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff,
    0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0,
    0x74
}};

const esp_phy_init_data_t *esp_phy_get_init_data(void) {
    return &phy_init_data;
}
