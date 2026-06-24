#!/bin/bash
set -e

ALPINE_VERSION="3.19.1"
ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v3.19/releases/x86_64/alpine-minirootfs-${ALPINE_VERSION}-x86_64.tar.gz"
TARBALL="alpine-minirootfs.tar.gz"

if [ -d "rootfs" ] && [ -n "$(ls -A rootfs)" ]; then
    echo "rootfs already exists, skipping."
    exit 0
fi

echo "Downloading Alpine minirootfs ${ALPINE_VERSION}..."
curl -L -o "$TARBALL" "$ALPINE_URL"

echo "Extracting..."
mkdir -p rootfs
sudo tar -xzf "$TARBALL" -C rootfs

rm "$TARBALL"
echo "Done. rootfs ready."
