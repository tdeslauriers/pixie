#!/bin/bash

docker build -t pixie .

docker run -d --rm -p $(op read "op://world_site/pixie_service_container_dev/port"):$(op read "op://world_site/pixie_service_container_dev/port") \
    -e PIXIE_SERVICE_CLIENT_ID=$(op read "op://world_site/pixie_service_container_dev/client_id") \
    -e PIXIE_SERVICE_PORT=":$(op read "op://world_site/pixie_service_container_dev/port")" \
    -e PIXIE_CA_CERT="$(op document get "service_ca_dev_cert" --vault world_site | base64 -w 0)" \
    -e PIXIE_SERVER_CERT="$(op document get "pixie_service_server_dev_cert" --vault world_site | base64 -w 0)" \
    -e PIXIE_SERVER_KEY="$(op document get "pixie_service_server_dev_key" --vault world_site | base64 -w 0)" \
    -e PIXIE_CLIENT_CERT="$(op document get "pixie_service_client_dev_cert" --vault world_site | base64 -w 0)" \
    -e PIXIE_CLIENT_KEY="$(op document get "pixie_service_client_dev_key" --vault world_site | base64 -w 0)" \
    -e PIXIE_OBJECT_STORAGE_URL="$(op read "op://world_site/pixie_minio_dev/url")" \
    -e PIXIE_OBJECT_STORAGE_BUCKET="$(op read "op://world_site/pixie_minio_dev/bucket")" \
    -e PIXIE_OBJECT_STORAGE_ACCESS_KEY="$(op read "op://world_site/pixie_minio_dev/username")" \
    -e PIXIE_OBJECT_STORAGE_SECRET_KEY="$(op read "op://world_site/pixie_minio_dev/password")" \
    -e PIXIE_S2S_AUTH_URL="$(op read "op://world_site/ran_service_container_dev/url"):$(op read "op://world_site/ran_service_container_dev/port")" \
    -e PIXIE_S2S_AUTH_CLIENT_ID="$(op read "op://world_site/pixie_service_container_dev/client_id")" \
    -e PIXIE_S2S_AUTH_CLIENT_SECRET="$(op read "op://world_site/pixie_service_container_dev/password")" \
    -e PIXIE_DB_CA_CERT="$(op document get "db_ca_dev_cert" --vault world_site | base64 -w 0)" \
    -e PIXIE_DB_CLIENT_CERT="$(op document get "pixie_db_client_dev_cert" --vault world_site | base64 -w 0)" \
    -e PIXIE_DB_CLIENT_KEY="$(op document get "pixie_db_client_dev_key" --vault world_site | base64 -w 0)" \
    -e PIXIE_DATABASE_URL="$(op read "op://world_site/pixie_db_dev/server"):$(op read "op://world_site/pixie_db_dev/port")" \
    -e PIXIE_DATABASE_NAME="$(op read "op://world_site/pixie_db_dev/database")" \
    -e PIXIE_DATABASE_USERNAME="$(op read "op://world_site/pixie_db_dev/username")" \
    -e PIXIE_DATABASE_PASSWORD="$(op read "op://world_site/pixie_db_dev/password")" \
    -e PIXIE_DATABASE_HMAC_INDEX_SECRET="$(op read "op://world_site/pixie_hmac_index_secret_dev/secret")" \
    -e PIXIE_FIELD_LEVEL_AES_GCM_SECRET="$(op read "op://world_site/pixie_aes_gcm_secret_dev/secret")" \
    -e PIXIE_S2S_JWT_VERIFYING_KEY="$(op read "op://world_site/ran_jwt_key_pair_dev/verifying_key")" \
    -e PIXIE_USER_JWT_VERIFYING_KEY="$(op read "op://world_site/shaw_jwt_key_pair_dev/verifying_key")" \
    pixie:latest
