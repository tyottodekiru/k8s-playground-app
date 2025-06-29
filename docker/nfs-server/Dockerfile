# docker/nfs-server/Dockerfile
FROM ubuntu:22.04

# NFSサーバーと関連ツールをインストール
RUN apt-get update && \
    apt-get install -y --no-install-recommends nfs-kernel-server rpcbind && \
    rm -rf /var/lib/apt/lists/*

# 共有ディレクトリの作成
RUN mkdir -p /exports && chmod 777 /exports

# エントリーポイントスクリプトをコピーし、実行権限を付与
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# NFS関連ポートを開放
EXPOSE 2049/tcp 111/tcp 111/udp 20048/tcp 20048/udp

# コンテナ起動時にスクリプトを実行
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
