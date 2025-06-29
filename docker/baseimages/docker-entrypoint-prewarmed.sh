#!/bin/bash
set -e

# dockerd と kind クラスタを supervisord 経由で起動
if [ "$1" = 'supervisord' ]; then
    # supervisordをバックグラウンドで起動し、dockerdを開始
    echo "Starting supervisord in background..."
    /usr/bin/supervisord -c /etc/supervisor/conf.d/supervisord.conf &

    # Dockerデーモンが起動するまで待機
    echo "Waiting for Docker daemon to be available..."
    retries=20
    while ! docker info > /dev/null 2>&1; do
        sleep 1
        retries=$((retries - 1))
        if [ $retries -le 0 ]; then
            echo "Docker daemon did not start in time." >&2
            exit 1
        fi
    done
    echo "Docker daemon is ready."

    # ★★★ ここでtarからイメージをロードする ★★★
    if [ -f /opt/prewarmed-images/node.tar ]; then
        echo "Loading pre-warmed kindest/node image from tarball..."
        docker load < /opt/prewarmed-images/node.tar
        rm /opt/prewarmed-images/node.tar
        echo "Pre-warmed image loaded successfully."
    fi

    # kindクラスタが存在しない場合のみ作成する
    if ! kind get clusters | grep -q "kind"; then
        echo "Creating kind cluster..."
        # --waitでコントロールプレーンが準備完了するまで待機する
        kind create cluster --name kind --config /etc/kind/kind-config.yaml --wait 5m
        echo "Kind cluster created successfully."
        echo "You can now exec into the container and use kubectl."
    else
        echo "Kind cluster already exists. Skipping creation."
    fi

    # フォアグラウンドで待機し、コンテナが終了しないようにする
    wait -n
    exit $?
fi

# 渡されたコマンドを実行 (例: bash)
exec "$@"
