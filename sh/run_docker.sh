#!/bin/bash

set -euo pipefail

IMAGE_NAME="pixie:latest"
CONTAINER_NAME="pixie-dev"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

docker build --pull --no-cache -f "${REPO_ROOT}/Dockerfile" -t "${IMAGE_NAME}" "${REPO_ROOT}"

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

docker run -d --rm --name "${CONTAINER_NAME}" -p "${PIXIE_SERVICE_PORT: -4}":"${PIXIE_SERVICE_PORT: -4}" \
    -e PIXIE_SERVICE_CLIENT_ID \
    -e PIXIE_SERVICE_PORT \
    -e PIXIE_CA_CERT \
    -e PIXIE_SERVER_CERT \
    -e PIXIE_SERVER_KEY \
    -e PIXIE_CLIENT_CERT \
    -e PIXIE_CLIENT_KEY \
    -e PIXIE_OBJECT_STORAGE_URL \
    -e PIXIE_OBJECT_STORAGE_BUCKET \
    -e PIXIE_OBJECT_STORAGE_ACCESS_KEY \
    -e PIXIE_OBJECT_STORAGE_SECRET_KEY \
    -e PIXIE_S2S_AUTH_URL \
    -e PIXIE_S2S_AUTH_CLIENT_ID \
    -e PIXIE_S2S_AUTH_CLIENT_SECRET \
    -e PIXIE_DB_CA_CERT \
    -e PIXIE_DB_CLIENT_CERT \
    -e PIXIE_DB_CLIENT_KEY \
    -e PIXIE_DATABASE_URL \
    -e PIXIE_DATABASE_NAME \
    -e PIXIE_DATABASE_USERNAME \
    -e PIXIE_DATABASE_PASSWORD \
    -e PIXIE_DATABASE_HMAC_INDEX_SECRET \
    -e PIXIE_FIELD_LEVEL_AES_GCM_SECRET \
    -e PIXIE_S2S_JWT_VERIFYING_KEY \
    -e PIXIE_USER_JWT_VERIFYING_KEY \
    "${IMAGE_NAME}"
