# How to start WiFi

## Call sequence

1. **Once at startup:** `espradio.Enable(config)`
   - Inside: hardware reset, `esp_wifi_internal_set_log_level`, `espradio_wifi_init()`:
     - `esp_wifi_init_internal(&cfg)` — allocates buffers, starts the driver task
     - `wifi_init_completed()` — driver marks itself as "initialized" (esp_supplicant_init is not called — linking WPA sources pulls in gettimeofday, esp_event_post, etc.)
   - Then wait up to 2 seconds until `esp_wifi_set_mode(STA)` returns OK (flag check).

2. **Scan networks:** `espradio.Scan()`
   - `esp_wifi_set_mode(WIFI_MODE_STA)`
   - `esp_wifi_start()`
   - `esp_wifi_scan_start(nil, true)`
   - `esp_wifi_scan_get_ap_num` / `esp_wifi_scan_get_ap_records`

3. **Connect to an AP** (if needed): same `set_mode`/`start`, then `esp_wifi_set_config` + `esp_wifi_connect()` (no wrapper in code yet).

## Example

```go
err := espradio.Enable(espradio.Config{Logging: espradio.LogLevelInfo})
if err != nil {
    // 12289 = NOT_INIT: failed to set the initialization flag (see radio.c)
    return
}
aps, err := espradio.Scan()
// ...
```

## If Enable() returns "wifi not initialized" (12289)

- Make sure the linker script matches IDF (DRAM/IRAM 0x4E710, see TinyGo targets).
- After `espradio_wifi_init()` the code calls `wifi_init_completed()`; without this the driver does not consider itself initialized. The build should use the prebuilt `libwpa_supplicant.a` from blobs instead of sources (otherwise the linker will request esp_event_post, gettimeofday, esp_fill_random, etc.).
