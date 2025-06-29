#!/bin/bash
set -e

# --- 静的ポート設定 ---
LOCKD_PORT=32768
MOUNTD_PORT=32767
STATD_PORT=32765

# 1. lockd (NLM) のためのカーネルパラメータを設定
echo "[NFS Entrypoint] Configuring lockd kernel module ports..."
echo $LOCKD_PORT > /proc/sys/fs/nfs/nlm_tcpport
echo $LOCKD_PORT > /proc/sys/fs/nfs/nlm_udpport
echo "[NFS Entrypoint] lockd ports set to $LOCKD_PORT"

# 2. /etc/exports を設定
SHARED_DIR=${SHARED_DIRECTORY:-/exports}
mkdir -p "$SHARED_DIR"
chmod 777 "$SHARED_DIR"
# fsid=0 はNFSv4のルートを示すために必須
echo "$SHARED_DIR *(rw,sync,fsid=0,no_subtree_check,no_root_squash,insecure)" > /etc/exports

echo "--- /etc/exports ---"
cat /etc/exports
echo "--------------------"

# 3. 各種サービスを開始
echo "Starting rpcbind..."
/usr/sbin/rpcbind -w

echo "Starting rpc.statd on port $STATD_PORT..."
/usr/sbin/rpc.statd --port $STATD_PORT --no-notify

echo "Starting NFS kernel daemon..."
/usr/sbin/rpc.nfsd 8 # 8スレッドで起動

echo "Starting rpc.mountd on port $MOUNTD_PORT..."
/usr/sbin/rpc.mountd --port $MOUNTD_PORT

# 4. 設定をエクスポート
echo "Exporting file systems..."
/usr/sbin/exportfs -ra

echo "NFS Server is running with static ports."

# コンテナを終了させない
tail -f /dev/null
