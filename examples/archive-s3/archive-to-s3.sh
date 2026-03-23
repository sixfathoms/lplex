#!/usr/bin/env bash
#
# archive-to-s3.sh — lplex journal archive script for Amazon S3
#
# This script implements the lplex archive JSONL protocol:
#   - Receives file paths as positional args
#   - Reads per-file metadata from stdin (JSONL)
#   - Uploads each file to S3 using the AWS CLI
#   - Writes per-file status to stdout (JSONL)
#
# Usage in lplex-server.conf:
#   journal.archive.command = /usr/local/bin/archive-to-s3.sh
#   journal.archive.trigger = on-rotate
#
# Environment variables:
#   S3_BUCKET       — S3 bucket name (required)
#   S3_PREFIX       — key prefix (default: "lplex/journals/")
#   INSTANCE_ID     — boat identifier for S3 key (default: hostname)
#   AWS_PROFILE     — AWS CLI profile (optional)
#   AWS_REGION      — AWS region (optional, uses CLI default)
#
# Prerequisites:
#   - AWS CLI v2 installed and configured (aws configure)
#   - jq installed (for JSON construction)
#   - IAM permissions: s3:PutObject on the target bucket/prefix
#
# S3 key format:
#   s3://{bucket}/{prefix}{instance_id}/{filename}
#
# Example:
#   s3://my-boat-data/lplex/journals/inuc1/nmea2k-20260315T100000.000Z.lpj

set -euo pipefail

: "${S3_BUCKET:?S3_BUCKET environment variable is required}"
: "${S3_PREFIX:=lplex/journals/}"
: "${INSTANCE_ID:=$(hostname)}"

# Consume stdin metadata (one JSONL line per file).
# We don't need the metadata for S3 upload, but we must read it
# to avoid blocking the keeper.
cat > /dev/null

# Upload each file to S3 and emit JSONL status.
for filepath in "$@"; do
    filename=$(basename "$filepath")
    s3_key="${S3_PREFIX}${INSTANCE_ID}/${filename}"

    if err=$(aws s3 cp "$filepath" "s3://${S3_BUCKET}/${s3_key}" --no-progress 2>&1); then
        jq -nc --arg path "$filepath" '{path: $path, status: "ok"}'
    else
        jq -nc --arg path "$filepath" --arg err "$err" '{path: $path, status: "error", error: $err}'
    fi
done
