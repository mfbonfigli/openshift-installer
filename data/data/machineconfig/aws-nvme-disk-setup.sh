#!/bin/bash
# Discovers an EBS volume on an AWS Nitro instance by its block device mapping
# name, then partitions and formats it. Nitro instances expose EBS volumes as
# NVMe devices with non-deterministic naming; the traditional /dev/sd* symlinks
# are not available during early boot, so the script reads the vendor-specific
# data via "nvme amzn id-ctrl" to match the intended device.
#
# Usage: aws-nvme-disk-setup.sh <bdev> <label>
#   bdev  - block device mapping name without /dev/ prefix (e.g. "sdb")
#   label - partition label (e.g. "etcd")

set -euo pipefail

TARGET_BDEV="$1"
LABEL="$2"

if blkid -t PARTLABEL="$LABEL" > /dev/null 2>&1; then
  echo "Partition $LABEL already exists, skipping setup"
  exit 0
fi

DEVICE=""
for dev in /dev/nvme*n1; do
  [ -b "$dev" ] || continue
  bdev=$(nvme amzn id-ctrl "$dev" 2>/dev/null | awk '/^bdev/{print $3}') || continue
  if [ "$bdev" = "$TARGET_BDEV" ]; then
    DEVICE="$dev"
    break
  fi
done

if [ -z "$DEVICE" ]; then
  echo "ERROR: no NVMe device found with bdev=$TARGET_BDEV" >&2
  exit 1
fi

echo "Found $DEVICE for bdev=$TARGET_BDEV, setting up as $LABEL"

# Wipe existing partition table
sfdisk --delete "$DEVICE" 2>/dev/null || true
wipefs -a "$DEVICE"

echo "start=2048, name=$LABEL" | sfdisk --label gpt "$DEVICE"
udevadm settle
PART="/dev/disk/by-partlabel/$LABEL"
mkfs.xfs -f -L "$LABEL" "$PART"
