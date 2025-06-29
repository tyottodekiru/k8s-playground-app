# syntax=docker/dockerfile:1

# --- Builder Stage ---
# Goのバージョンをgo.modの指定に合わせる (例: 1.24)
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 1. go.mod と go.sum をコピーして、基本的な依存関係をダウンロード
COPY go.mod go.sum ./
RUN go mod download

# 2. アプリケーションの全ソースコードをコピー
COPY . .

# 3. 全ソースコードをコピーした後で go mod tidy を実行し、
#    go.mod と go.sum を最終的な状態に更新
RUN go mod tidy

# 4. アプリケーションをビルド
#    -o /app-controller で出力先を/app/app-controllerに指定
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app-controller ./cmd/app-controller

# --- Final Stage ---
FROM alpine:latest

# セキュリティアップデートと証明書の追加
RUN apk update && apk add --no-cache ca-certificates && update-ca-certificates

WORKDIR /app

# 静的アセットとテンプレートをコピー
COPY --from=builder /app/web/static ./web/static
COPY --from=builder /app/web/templates ./web/templates

# ビルドされたバイナリをビルダー段階からコピー
COPY --from=builder /app-controller /app-controller

# ポートを公開 (Ginのデフォルトは8080ですが、PORT環境変数で上書き可能)
EXPOSE 8080

# アプリケーション実行コマンド
# 実際のポートはデプロイ時のPORT環境変数によって決定されます
ENTRYPOINT ["/app-controller"]

