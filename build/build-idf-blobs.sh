#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BLOBS_LIB="${ROOT_DIR}/blobs/libs/esp32c3"
IDF_PROJECT="${ROOT_DIR}/build/idf-wifi-project"
TINYGO_LD_LOCAL="${ROOT_DIR}/../origin_tinygo/targets/esp32c3.ld"
TINYGO_LD_DEST="${ROOT_DIR}/build/esp32c3.ld"
TINYGO_LD_URL="${TINYGO_LD_URL:-https://raw.githubusercontent.com/tinygo-org/tinygo/master/targets/esp32c3.ld}"
IDF_PATH="${IDF_PATH:-${ROOT_DIR}/build/esp-idf}"
PYTHON_BIN="${PYTHON_BIN:-python3}"
if [ -z "${IDF_PATH:-}" ]; then
  echo "IDF_PATH not set. Run: . build/esp-idf/export.sh"
  exit 1
fi

if [ ! -f "${TINYGO_LD_DEST}" ]; then
  if [ -f "${TINYGO_LD_LOCAL}" ]; then
    echo "Copying TinyGo esp32c3.ld from local origin_tinygo..."
    cp "${TINYGO_LD_LOCAL}" "${TINYGO_LD_DEST}"
  else
    echo "Downloading TinyGo esp32c3.ld from ${TINYGO_LD_URL}..."
    tmp_ld="${TINYGO_LD_DEST}.tmp"
    if curl -fsSL "${TINYGO_LD_URL}" -o "${tmp_ld}"; then
      mv "${tmp_ld}" "${TINYGO_LD_DEST}"
    else
      echo "Warning: failed to download esp32c3.ld, continue without local copy." >&2
      rm -f "${tmp_ld}"
    fi
  fi
fi
if [ -f "${TINYGO_LD_DEST}" ]; then
  mkdir -p "${ROOT_DIR}/blobs"
  cp -p "${TINYGO_LD_DEST}" "${ROOT_DIR}/blobs/esp32c3.ld"
  echo "Linker script synced to blobs/esp32c3.ld (TinyGo has IDF layout 0x4E710)"
fi

IDF_WIFI_LIB="${IDF_PATH}/components/esp_wifi/lib/esp32c3"
IDF_PHY_LIB="${IDF_PATH}/components/esp_phy/lib/esp32c3"

# Prebuilt WiFi/PHY libs are from Espressif. TinyGo's esp32c3.ld uses IDF layout (0x4E710)
# so memprot and WiFi blobs see the same DRAM/IRAM boundaries.

mkdir -p "${BLOBS_LIB}"
rm -f "${BLOBS_LIB}"/*.a

echo "Copying prebuilt from IDF esp_wifi..."
for f in "${IDF_WIFI_LIB}"/lib*.a; do
  [ -f "$f" ] && cp "$f" "${BLOBS_LIB}/"
done

echo "Copying prebuilt from IDF esp_phy..."
for f in "${IDF_PHY_LIB}"/lib*.a; do
  [ -f "$f" ] && cp "$f" "${BLOBS_LIB}/"
done

echo "Building IDF wifi project (for reference)..."
cd "${IDF_PROJECT}"
"${PYTHON_BIN}" "${IDF_PATH}/tools/idf.py" set-target esp32c3 2>/dev/null || true
"${PYTHON_BIN}" "${IDF_PATH}/tools/idf.py" build

echo "Syncing headers from esp-wifi into blobs..."
rm -rf "${ROOT_DIR}/blobs/headers" "${ROOT_DIR}/blobs/include"
if [ -d "${ROOT_DIR}/esp-wifi/c/headers" ]; then
  cp -rp "${ROOT_DIR}/esp-wifi/c/headers" "${ROOT_DIR}/blobs"
fi
if [ -d "${ROOT_DIR}/esp-wifi/c/include" ]; then
  cp -rp "${ROOT_DIR}/esp-wifi/c/include" "${ROOT_DIR}/blobs"
fi

echo "Overriding WiFi headers with ESP-IDF versions to match blobs (layout of wifi_init_config_t)..."
mkdir -p "${ROOT_DIR}/blobs/include/esp_private"
for h in esp_wifi.h esp_wifi_crypto_types.h; do
  if [ -f "${IDF_PATH}/components/esp_wifi/include/${h}" ]; then
    cp -p "${IDF_PATH}/components/esp_wifi/include/${h}" "${ROOT_DIR}/blobs/include/${h}"
  fi
done
if [ -f "${IDF_PATH}/components/esp_wifi/include/esp_private/wifi_os_adapter.h" ]; then
  cp -p "${IDF_PATH}/components/esp_wifi/include/esp_private/wifi_os_adapter.h" "${ROOT_DIR}/blobs/include/esp_private/wifi_os_adapter.h"
fi

if [ -d "${ROOT_DIR}/esp-wifi/esp-wifi-sys-esp32c3/libs" ]; then
  for name in wpa_supplicant regulatory printf; do
    if [ -f "${ROOT_DIR}/esp-wifi/esp-wifi-sys-esp32c3/libs/lib${name}.a" ]; then
      echo "Copying lib${name}.a from esp-wifi (override/extend IDF blobs)..."
      cp "${ROOT_DIR}/esp-wifi/esp-wifi-sys-esp32c3/libs/lib${name}.a" "${BLOBS_LIB}/"
    fi
  done
fi

echo "Blobs in ${BLOBS_LIB}:"
ls -la "${BLOBS_LIB}"/*.a
