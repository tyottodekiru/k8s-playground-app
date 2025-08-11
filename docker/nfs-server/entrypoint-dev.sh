#!/bin/bash
set -e

# --- Static port configuration ---
LOCKD_PORT=32768
MOUNTD_PORT=32767
STATD_PORT=32765

# Ensure /exports directory exists and is writable
SHARED_DIR=${SHARED_DIRECTORY:-/exports}
mkdir -p "$SHARED_DIR"
chmod 777 "$SHARED_DIR"

# For development: Mount tmpfs if /exports is not already a tmpfs
if ! mountpoint -q "$SHARED_DIR"; then
    echo "Mounting tmpfs for development NFS exports..."
    mount -t tmpfs tmpfs "$SHARED_DIR" || {
        echo "Warning: Could not mount tmpfs, using regular directory"
    }
fi

# Set up /etc/exports with less restrictive options for development
echo "$SHARED_DIR *(rw,sync,fsid=0,no_subtree_check,no_root_squash,insecure,no_wdelay)" > /etc/exports

echo "--- /etc/exports ---"
cat /etc/exports
echo "--------------------"

# Start services
echo "Starting rpcbind..."
/usr/sbin/rpcbind -w

echo "Starting rpc.statd on port $STATD_PORT..."
/usr/sbin/rpc.statd --port $STATD_PORT --no-notify

echo "Starting NFS kernel daemon..."
/usr/sbin/rpc.nfsd 8

echo "Starting rpc.mountd on port $MOUNTD_PORT..."
/usr/sbin/rpc.mountd --port $MOUNTD_PORT

# Export filesystems
echo "Exporting file systems..."
/usr/sbin/exportfs -ra

echo "NFS Server is running for development with static ports."
echo "Shared directory: $SHARED_DIR"

# Create a test file
echo "NFS server ready - $(date)" > "$SHARED_DIR/nfs-ready.txt"

# Keep container running
tail -f /dev/null