#!/bin/bash

# variables
NAMESPACE="world"
SECRET_NAME="secret-gallery-db"

# get db secrets from 1Password
DB_PASSWORD=$(op read "op://world_site/pixie_db_prod/password")
HMAC_INDEX_SECRET=$(op read "op://world_site/pixie_hmac_index_secret_prod/secret")
AES_GCM_SECRET=$(op read "op://world_site/pixie_aes_gcm_secret_prod/secret")

# check if values are retrieved successfully
if [[ -z "$DB_PASSWORD" || -z "$HMAC_INDEX_SECRET" || -z "$AES_GCM_SECRET" ]]; then
  echo "Error: failed to get pixie db secrets from 1Password."
  exit 1
fi

# create the db secret
kubectl create secret generic $SECRET_NAME \
  --namespace $NAMESPACE \
  --from-literal=db-password="$DB_PASSWORD" \
  --from-literal=hmac-index-secret="$HMAC_INDEX_SECRET" \
  --from-literal=aes-gcm-secret="$AES_GCM_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f -
