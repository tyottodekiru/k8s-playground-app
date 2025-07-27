FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV DOCKER_TLS_CERTDIR=/certs

ARG K8S_VERSION
ENV K8S_VERSION=${K8S_VERSION}

# 必要なツールのインストール
RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    gnupg \
    lsb-release \
    software-properties-common \
    build-essential \
    bash \
    dnsutils \
    iproute2 \
    vim \
    tmux \
    iputils-ping \
    wget \
    unzip

# Dockerの公式GPGキーを追加
RUN install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
        gpg --dearmor -o /etc/apt/keyrings/docker.gpg && \
    chmod a+r /etc/apt/keyrings/docker.gpg

# Dockerのレポジトリを追加
RUN echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" \
        > /etc/apt/sources.list.d/docker.list

# Dockerのインストール
RUN apt-get update && apt-get install -y \
    docker-ce \
    docker-ce-cli \
    containerd.io \
    docker-buildx-plugin \
    docker-compose-plugin

# TLSディレクトリの準備（rootless docker対応）
RUN mkdir -p /certs/client && chmod 1777 /certs /certs/client

# kubectl のインストール
RUN KUBECTL_VERSION="$(curl -Ls https://dl.k8s.io/release/stable.txt)" && \
    curl -LO "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/


# kindのインストール（ビルド済みバイナリ）
RUN curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64 && \
    chmod +x ./kind && mv ./kind /usr/local/bin/kind

# grpcurlのインストール
RUN GRPCURL_VERSION="1.9.1" && \
    curl -Lo grpcurl.tar.gz "https://github.com/fullstorydev/grpcurl/releases/download/v${GRPCURL_VERSION}/grpcurl_${GRPCURL_VERSION}_linux_x86_64.tar.gz" && \
    tar -xzf grpcurl.tar.gz && \
    mv grpcurl /usr/local/bin/ && \
    rm grpcurl.tar.gz

# kind用のクラスタ設定テンプレートを追加（必要に応じて上書き可能）
COPY docker-entrypoint.sh /usr/local/bin/
COPY update-kube-config.sh /usr/local/bin/
COPY kind-config.yaml /etc/
RUN echo 'alias k=/usr/local/bin/kubectl' >> /root/.bashrc
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /usr/local/bin/update-kube-config.sh

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["bash"]

