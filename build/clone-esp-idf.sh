#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IDF_DIR="${ROOT_DIR}/build/esp-idf"
IDF_VERSION="${IDF_VERSION:-v5.2.2}"

if [ -d "${IDF_DIR}/.git" ]; then
  echo "ESP-IDF already cloned at ${IDF_DIR}"
  (cd "${IDF_DIR}" && git fetch --tags && git checkout "${IDF_VERSION}")
  echo "Run: cd ${IDF_DIR} && ./install.sh esp32c3"
  exit 0
fi

echo "Cloning ESP-IDF ${IDF_VERSION} to ${IDF_DIR}"
mkdir -p "${ROOT_DIR}/build"
git clone --depth 1 --branch "${IDF_VERSION}" https://github.com/espressif/esp-idf.git "${IDF_DIR}"

echo "Install toolchain: cd ${IDF_DIR} && ./install.sh esp32c3"
echo "Then: . ${IDF_DIR}/export.sh"
