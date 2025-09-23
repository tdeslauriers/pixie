#!/bin/bash

# variables
NAMESPACE="world"
SECRET_NAME="secret-pixie-server-cert"

# get certificate and key from 1Password
SERVER_CERT=$(op document get "pixie_service_server_prod_cert" --vault world_site | base64 -w 0)
SERVER_KEY=$(op document get "pixie_service_server_prod_key" --vault world_site | base64 -w 0)

# check if values are retrieved successfully
if [[ -z "$SERVER_CERT" || -z "$SERVER_KEY" ]]; then
  echo "Error: failed to get pixie prod server certificate or key from 1Password."
  exit 1
fi

# create the TLS secret --> note: using generic secret type because injecting as base64 encoded string to app
kubectl create secret generic $SECRET_NAME \
  --namespace $NAMESPACE \
  --from-literal=server-cert="$SERVER_CERT" \
  --from-literal=server-key="$SERVER_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
