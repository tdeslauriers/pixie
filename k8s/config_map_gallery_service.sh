#!/bin/bash

# Set the namespace and ConfigMap name
NAMESPACE="world"
CONFIG_MAP_NAME="cm-gallery-service"

# get url, port, and client id from 1password
GALLERY_URL=$(op read "op://world_site/pixie_service_container_prod/url")
GALLERY_PORT=$(op read "op://world_site/pixie_service_container_prod/port")
GALLERY_CLIENT_ID=$(op read "op://world_site/pixie_service_container_prod/client_id")

# validate values are not empty
if [[ -z "$GALLERY_URL" || -z "$GALLERY_PORT" || -z "$GALLERY_CLIENT_ID" ]]; then
  echo "Error: failed to get gallery config vars from 1Password."
  exit 1
fi

# generate cm yaml and apply
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: $CONFIG_MAP_NAME
  namespace: $NAMESPACE
data:
  gallery-url: "$GALLERY_URL:$GALLERY_PORT"
  gallery-port: ":$GALLERY_PORT"
  gallery-client-id: "$GALLERY_CLIENT_ID"
EOF
