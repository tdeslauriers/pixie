#!/bin/bash

# variables
NAMESPACE="world"
SECRET_NAME="secret-pixie-db-client-cert"

# get certificate and key from 1Password
CLIENT_CERT=$(op document get "pixie_db_client_prod_cert" --vault world_site | base64 -w 0)
CLIENT_KEY=$(op document get "pixie_db_client_prod_key" --vault world_site | base64 -w 0)

# check if values are retrieved successfully
if [[ -z "$CLIENT_CERT" || -z "$CLIENT_KEY" ]]; then
  echo "Error: failed to get pixie prod db client certificate or key from 1Password."
  exit 1
fi

# create the TLS secret --> note: using generic secret type because injecting as base64 encoded string to app
kubectl create secret generic $SECRET_NAME \
  --namespace $NAMESPACE \
  --from-literal=client-cert="$CLIENT_CERT" \
  --from-literal=client-key="$CLIENT_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -

