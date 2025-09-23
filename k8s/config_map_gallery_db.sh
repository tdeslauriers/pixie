#!/bin/bash

# Set the namespace and ConfigMap name
NAMESPACE="world"
CONFIG_MAP_NAME="cm-gallery-db"

# get config from 1password
DB_URL=$(op read "op://world_site/pixie_db_prod/server")
DB_PORT=$(op read "op://world_site/pixie_db_prod/port")
DB_NAME=$(op read "op://world_site/pixie_db_prod/database")
DB_USERNAME=$(op read "op://world_site/pixie_db_prod/username")

# validate values are not empty
if [[ -z "$DB_URL" || -z "$DB_PORT" || -z "$DB_NAME" || -z "$DB_USERNAME" ]]; then
  echo "Error: failed to get gallery db config vars from 1Password."
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
  db-url: "$DB_URL:$DB_PORT"
  db-name: "$DB_NAME"
  db-username: "$DB_USERNAME"
EOF

