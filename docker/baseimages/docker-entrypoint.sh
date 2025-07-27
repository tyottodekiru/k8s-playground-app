#!/bin/sh
# エラー発生時にスクリプトを終了する
set -e

# Dockerデーモンをバックグラウンドで起動し、ログをファイルに出力
# --host=unix:///var/run/docker.sock を指定してUnixソケットをリッスンさせる
dockerd --host=unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &

# Dockerデーモンが起動するまで待機する
echo "[INFO] Waiting for Docker daemon to start..."
tries=0
# タイムアウト（約30秒）を設定
while ! docker info >/dev/null 2>&1; do
  if [ "$tries" -gt 30 ]; then
    echo "[ERROR] Docker daemon failed to start. Check logs below."
    cat /var/log/dockerd.log
    exit 1
  fi
  tries=$((tries + 1))
  sleep 1
done
echo "[INFO] Docker daemon started successfully."

# K8S_VERSIONが設定されていない場合はエラーで終了
if [ -z "${K8S_VERSION}" ]; then
  echo "[ERROR] K8S_VERSION environment variable is not set."
  exit 1
fi

# kindクラスタが存在しない場合に作成
if ! kind get clusters | grep -q "^dev-cluster$"; then
  echo "[INFO] kind cluster 'dev-cluster' not found. Creating..."
  kind create cluster \
    --name dev-cluster \
    --config /etc/kind-config.yaml \
    --image "kindest/node:${K8S_VERSION}" 
else
  echo "[INFO] kind cluster 'dev-cluster' already exists. Skipping creation."
fi

# kubectl context のデフォルト namespace を default に設定
echo "[INFO] Setting default namespace to 'default'..."
kubectl config set-context --current --namespace=default

# コンテナを起動状態で維持（kubectl exec で接続するため）
echo "[INFO] Setup completed. Container will keep running for kubectl exec access."
exec sleep infinity
