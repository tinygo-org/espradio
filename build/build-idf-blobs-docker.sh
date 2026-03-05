#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${IDF_DOCKER_IMAGE:-espressif/idf:v5.2.2}"

echo "Using ESP-IDF Docker image: ${IMAGE}"

docker run --rm \
  -v "${ROOT_DIR}":/project \
  -w /project \
  "${IMAGE}" \
  bash -lc '
set -euo pipefail
export ROOT_DIR=/project
export IDF_PATH=/opt/esp/idf
export PYTHON_BIN=python
cd "${ROOT_DIR}"
./build/build-idf-blobs.sh
'

