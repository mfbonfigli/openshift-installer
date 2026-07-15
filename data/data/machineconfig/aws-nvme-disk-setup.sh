#!/bin/bash
# Discovers an EBS volume on an AWS Nitro instance by its block device mapping
# name, then partitions and formats it. Nitro instances expose EBS volumes as
# NVMe devices with non-deterministic naming; the traditional /dev/sd* symlinks
# are not available during early boot, so the script reads the vendor-specific
# data via "nvme amzn id-ctrl" to match the intended device.
#
# Usage: aws-nvme-disk-setup.sh <bdev> <label> <disk-type>
#   bdev      - block device mapping name without /dev/ prefix (e.g. "sdb")
#   label     - partition label (e.g. "etcd")
#   disk-type - one of: etcd, swap, userDefined

set -euo pipefail

TARGET_BDEV="$1"
LABEL="$2"
DISK_TYPE="$3"

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

echo "Found $DEVICE for bdev=$TARGET_BDEV, setting up as $LABEL ($DISK_TYPE)"

# Wipe existing partition table
sfdisk --delete "$DEVICE" 2>/dev/null || true
wipefs -a "$DEVICE"

case "$DISK_TYPE" in
  swap)
    echo "start=2048, type=0657FD6D-A4AB-43C4-84E5-0933C84B4F4F, name=$LABEL" | sfdisk --label gpt "$DEVICE"
    udevadm settle
    PART="/dev/disk/by-partlabel/$LABEL"
    mkswap -L "$LABEL" "$PART"
    ;;
  *)
    echo "start=2048, name=$LABEL" | sfdisk --label gpt "$DEVICE"
    udevadm settle
    PART="/dev/disk/by-partlabel/$LABEL"
    mkfs.xfs -f -L "$LABEL" "$PART"
    ;;
esac
