#!/bin/bash

# エラーが発生した場合、スクリプトを直ちに終了する
set -e

# ビルド対象とするKubernetesのバージョンリストを定義
# 各マイナーバージョンで利用可能な安定版のパッチバージョンを指定
VERSIONS=(
  "v1.30.2"
  "v1.31.2"
  "v1.32.1"
  "v1.33.0"
)

# ループ処理で各バージョンのイメージをビルド
for k8s_version in "${VERSIONS[@]}"; do
  # イメージタグに使われるバージョン部分を作成 (例: "v1.31.2" -> "1.31.2")
  # ${k8s_version#v} は、変数k8s_versionの先頭にある "v" を取り除く bash の機能
  image_tag_version="${k8s_version#v}"
  
  # これからビルドする情報を分かりやすく表示
  echo "======================================================================"
  echo ">>> Building image for Kubernetes ${k8s_version}"
  echo ">>> Image Tag: tyottodekiru/dind:k8s-${image_tag_version}"
  echo "======================================================================"
  
  # docker buildコマンドの実行
  docker build \
    --build-arg K8S_VERSION="${k8s_version}" \
    -t "tyottodekiru/dind:k8s-${image_tag_version}" \
    .
  echo "✅ Successfully built tyottodekiru/dind:k8s-${image_tag_version}"
  docker push "tyottodekiru/dind:k8s-${image_tag_version}"
  echo "✅ Successfully push tyottodekiru/dind:k8s-${image_tag_version}"
  echo
done

echo "🎉 All builds completed successfully!"
