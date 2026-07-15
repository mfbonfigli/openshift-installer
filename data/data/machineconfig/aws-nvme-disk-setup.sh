#!/bin/bash
# Discovers an EBS volume and partitions/formats it for use as a dedicated
# data disk. Works on both AWS Nitro (NVMe) and Xen-based instances.
#
# On Nitro instances, EBS volumes appear as /dev/nvme*n1 with non-deterministic
# ordering. AWS embeds the block device mapping name in the NVMe controller
# vendor-specific data; this script reads it via "nvme amzn id-ctrl".
#
# On Xen instances, the volume appears directly at the specified device path.
#
# Usage: aws-nvme-disk-setup.sh <bdev> <label>
#   bdev  - block device mapping name without /dev/ prefix (e.g. "xvdb")
#   label - partition label (e.g. "etcd")

set -euo pipefail

TARGET_BDEV="$1"
LABEL="$2"

if blkid -t PARTLABEL="$LABEL" > /dev/null 2>&1; then
  echo "Partition $LABEL already exists, skipping setup"
  exit 0
fi

DEVICE=""

# Xen instances: the device appears directly at /dev/$TARGET_BDEV
if [ -b "/dev/$TARGET_BDEV" ]; then
  DEVICE="/dev/$TARGET_BDEV"
else
  # Nitro instances: discover the NVMe device by matching the bdev field
  # in the NVMe controller vendor-specific data. Settle udev first to ensure
  # all NVMe devices are enumerated before we iterate.
  udevadm settle --timeout=120
  for dev in /dev/nvme*n1; do
    [ -b "$dev" ] || continue
    bdev=$(nvme amzn id-ctrl "$dev" 2>/dev/null | awk '/^bdev/{print $3}') || continue
    if [ "$bdev" = "$TARGET_BDEV" ]; then
      DEVICE="$dev"
      break
    fi
  done
fi

if [ -z "$DEVICE" ]; then
  echo "ERROR: no device found for bdev=$TARGET_BDEV" >&2
  exit 1
fi

echo "Found $DEVICE for bdev=$TARGET_BDEV, setting up as $LABEL"

# Wipe existing partition table
sfdisk --delete "$DEVICE" 2>/dev/null || true
wipefs -a "$DEVICE"

echo "start=2048, name=$LABEL" | sfdisk --label gpt "$DEVICE"
udevadm settle --timeout=120
PART="/dev/disk/by-partlabel/$LABEL"
mkfs.xfs -f -L "$LABEL" "$PART"
