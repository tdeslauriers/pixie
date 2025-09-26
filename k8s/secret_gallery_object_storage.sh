#!/bin/bash

# variables
NAMESPACE="world"
SECRET_NAME="secret-gallery-object-storage"

# get the secrets from 1Password
APP_KEY="$(op read "op://world_site/pixie_minio_prod/username")"
APP_SECRET="$(op read "op://world_site/pixie_minio_prod/password")"
ENDPOINT="$(op read "op://world_site/pixie_minio_prod/url")"
BUCKET="$(op read "op://world_site/pixie_minio_prod/bucket")"


# check if values are retrieved successfully
if [[ -z "$APP_KEY" || -z "$APP_SECRET"  || -z "$ENDPOINT" || -z "$BUCKET" ]]; then
  echo "Error: failed to minio app secrets from 1Password."
  exit 1
fi


# create the secret
kubectl -n world create secret generic $SECRET_NAME \
  --from-literal=ACCESS_KEY="$APP_KEY" \
  --from-literal=SECRET_KEY="$APP_SECRET" \
  --from-literal=S3_ENDPOINT="$ENDPOINT" \
  --from-literal=S3_BUCKET="$BUCKET" \
  --from-literal=REGION="us-east-1" \
  --from-literal=S3_FORCE_PATH_STYLE="true"