#!/bin/bash

# variables
NAMESPACE="world"
SECRET_NAME="secret-gallery-s2s-client-creds"

# get certificate and key from 1Password
S2S_AUTH_CLIENT_ID=$(op read "op://world_site/pixie_service_container_prod/client_id")
S2S_AUTH_CLIENT_SECRET=$(op read "op://world_site/pixie_service_container_prod/password")

# check if values are retrieved successfully
if [[ -z "$S2S_AUTH_CLIENT_ID" || -z "$S2S_AUTH_CLIENT_SECRET" ]]; then
  echo "Error: failed to get prod gallery service's s2s client credentials from 1Password."
  exit 1
fi

# create the s2s service creds secret
kubectl create secret generic $SECRET_NAME \
  --namespace $NAMESPACE \
  --from-literal=s2s-auth-client-id="$S2S_AUTH_CLIENT_ID" \
  --from-literal=s2s-auth-client-secret="$S2S_AUTH_CLIENT_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f -

