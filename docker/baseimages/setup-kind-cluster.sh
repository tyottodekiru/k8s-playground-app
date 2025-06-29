#!/bin/sh
set -e

echo "[INFO] Waiting for Docker daemon to be ready..."

# Dockerデーモンが起動するまで待機
tries=0
while ! docker info >/dev/null 2>&1; do
  if [ "$tries" -gt 60 ]; then
    echo "[ERROR] Docker daemon failed to start within 60 seconds."
    exit 1
  fi
  tries=$((tries + 1))
  sleep 1
done

echo "[INFO] Docker daemon is ready."

# K8S_VERSIONの確認
if [ -z "${K8S_VERSION}" ]; then
  echo "[ERROR] K8S_VERSION environment variable is not set."
  exit 1
fi

# kindクラスタが存在しない場合に作成
if ! kind get clusters 2>/dev/null | grep -q "^dev-cluster$"; then
  echo "[INFO] Creating kind cluster 'dev-cluster' with Kubernetes ${K8S_VERSION}..."
  kind create cluster \
    --name dev-cluster \
    --image "kindest/node:${K8S_VERSION}"
  echo "[INFO] Kind cluster 'dev-cluster' created successfully."
else
  echo "[INFO] Kind cluster 'dev-cluster' already exists."
fi

# kubeconfigの更新
if command -v /usr/local/bin/update-kube-config.sh >/dev/null 2>&1; then
  echo "[INFO] Updating kubeconfig..."
  /usr/local/bin/update-kube-config.sh
fi

echo "[INFO] Kind cluster setup completed."
