# Local Development Chart - Differences from Public Chart

このディレクトリ (`charts/k8s-playground-local/`) には、ローカル開発専用のHelmチャートが含まれています。
このチャートは [公開されているk8s-playground-chart](https://github.com/tyottodekiru/k8s-playground-chart) をベースにしていますが、ローカル開発のために以下の重要な変更が加えられています。

## 🎯 開発における課題と解決方針

### 従来の開発フローの問題点
1. **コード変更のたびにイメージ再ビルド**が必要で、開発サイクルが遅い
2. **複数のコントローラー**（app, generator, collector, killer, logging）の同時開発が困難
3. **NFSサーバーの起動**やユーザー環境のセットアップが複雑
4. **認証設定**（Google OAuth）でローカルテストが面倒

### 解決方針
- **バイナリマウント**によるホットリロード開発環境の実現
- **軽量コンテナ + ローカルビルド**での高速開発サイクル
- **アプリケーション実装の変更は絶対に行わない**制約の下での環境構築
- **公開チャートとの乖離を最小限**に抑えた設計

## 🔧 主な変更点

### 1. バイナリマウント対応（ホットリロード）
**変更箇所**: 各コントローラーのDeployment/StatefulSetテンプレート

**背景**: 
従来はコード変更→イメージビルド→ロード→デプロイの流れで5-10分かかっていた開発サイクルを、
Goのコンパイル（10-30秒）だけで済むように短縮したい。

**実装理由**:
- **hostPathボリューム**: kindクラスター内でホストの `/mnt/bin` ディレクトリを共有
- **軽量Alpine**: アプリケーション本体は含めず、最小限の実行環境のみ提供
- **実行時マウント**: コンテナ起動時に最新のローカルビルドバイナリを使用

**制約対応**:
アプリケーション実装変更禁止のため、バイナリパスを環境に合わせて調整

```yaml
# 例: app-controller
command: ["/mnt/bin/app-controller"]
volumeMounts:
  - name: binary-volume
    mountPath: /mnt/bin
volumes:
  - name: binary-volume
    hostPath:
      path: /mnt/bin
      type: Directory
```

### 2. Web資産マウント
**変更箇所**: app-controllerテンプレート

**背景**: 
HTMLテンプレート、CSS、JavaScript等のフロントエンド資産も頻繁に変更されるため、
バイナリと同様にホットリロードできる仕組みが必要。

**実装理由**:
- **hostPathマウント**: `/mnt/web` から `/app/web` への直接マウント
- **テンプレートパス修正**: `internal/controllers/app.go`で相対パス→絶対パスに変更済み

### 3. NFS サーバーのローカル開発対応
**変更箇所**: `templates/nfs-server-statefulset.yaml`

**背景**: 
アプリケーションコードは `/exports` パスをハードコードしており、NFSサーバーも本格的な
persistence機能は不要。ただし、アプリケーション実装は変更できない。

**実装理由**:
- **tmpfs使用**: メモリ上での高速動作、Pod再起動時のクリーンな状態
- **symlink戦略**: `/exports` → `/exports-tmpfs` で既存コードとの互換性確保
- **lifecycle hooks**: Pod起動時の自動symlink作成で運用の手間を削減

**技術的課題の解決**:
- NFSサーバーがtmpfsを使う場合、既存の `/exports` ディレクトリが存在
- `rm -rf /exports && ln -sf /exports-tmpfs /exports` でatomicに置換

```yaml
# ローカル開発時の追加設定
env:
- name: SHARED_DIRECTORY
  value: "/exports-tmpfs"  # tmpfsを使用
lifecycle:
  postStart:
    exec:
      command: ["/bin/sh", "-c", "rm -rf /exports && ln -sf /exports-tmpfs /exports || true"]
```

### 4. 認証方法の変更
**変更箇所**: `dev/values.yaml`

**背景**: 
Google OAuthはローカル開発時にコールバックURLの設定等が必要で開発の障壁となる。
開発者は機能開発に集中したい。

**実装理由**:
- **パスワード認証への切替**: アプリケーションが既に対応済み
- **固定認証情報**: `admin` / `admin123` で即座にログイン可能
- **セキュリティ**: あくまでローカル開発環境のみでの使用前提

### 5. リソース制限の調整
**変更箇所**: 各コントローラーのリソース設定

**背景**: 
開発者のラップトップ環境では本番相当のリソースは不要。むしろ軽量化して
複数の開発プロジェクトと共存できるようにしたい。

**実装理由**:
- **大幅なリソース削減**: memory 128Mi～512Mi、CPU 100m～500m程度
- **開発体験の優先**: 機能確認ができる最小限のリソース設定

```yaml
resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"  # 公開版より大幅削減
    cpu: "500m"
```

### 6. イメージプルポリシー
**変更箇所**: 全コントローラー

**背景**: 
kindクラスター環境ではレジストリから最新イメージをpullするのではなく、
ローカルでビルドしてロードしたイメージを使用したい。

**実装理由**:
- **`pullPolicy: "Never"`**: レジストリアクセスを完全に無効化
- **開発の高速化**: ネットワーク通信なしで即座にコンテナ起動
- **オフライン開発**: インターネット接続なしでも開発可能

### 7. 名前空間の統一
**変更箇所**: `dev/values.yaml`

**背景**: 
開発環境では全リソースを一つの名前空間に集約し、管理・削除を簡単にしたい。
公開チャートは本番環境でのマルチテナント運用を想定。

**実装理由**:
- **`k8s-playground` 名前空間**: 開発専用の独立した環境
- **リソース管理の簡素化**: `kubectl delete namespace k8s-playground` で全削除可能
- **干渉の回避**: 他の開発プロジェクトとの分離

## 📂 ファイル構造の違い

```
charts/k8s-playground-local/
├── Chart.yaml                    # 公開チャートと同一
├── values.yaml                   # 公開チャートと同一（デフォルト値）
├── templates/                    # 以下のファイルが変更されている
│   ├── deployment-app-controller.yaml      # バイナリマウント対応
│   ├── deployment-collector-controller.yaml
│   ├── deployment-generator-controller.yaml
│   ├── deployment-killer-controller.yaml
│   ├── deployment-logging-controller.yaml
│   ├── nfs-server-statefulset.yaml        # tmpfs + symlink対応
│   └── service.yaml                       # port-forward対応
└── DEVELOPMENT.md                # このファイル（新規追加）
```

## 🚀 使用方法

1. **開発環境のセットアップ**:
   ```bash
   make dev
   ```

2. **アプリケーションへのアクセス**:
   ```bash
   make port-forward
   # ブラウザで http://localhost:8080 にアクセス
   # ログイン: admin / admin123
   ```

3. **コード変更時の反映**:
   ```bash
   make build    # Goバイナリの再ビルドのみ
   # コンテナ再起動・イメージ再ビルド不要
   ```

## ⚠️ 注意事項

1. **本番環境では使用しないでください**: このチャートは開発専用です
2. **セキュリティ**: 固定パスワード、privileged コンテナを使用
3. **データ永続化**: NFS は tmpfs を使用するため、Pod 再起動時にデータが失われます
4. **アプリケーション実装は変更禁止**: アプリケーションコード自体は一切変更せず、Helmチャートレベルでの対応のみ

## 🔄 公開チャートとの同期

公開チャートに変更があった場合:
1. `values.yaml` と `Chart.yaml` は公開版と同期
2. `templates/` 内のファイルは上記変更点を保持しつつ同期
3. 新機能追加時は同様の開発対応を追加

---

このローカル開発チャートにより、コントリビューターは効率的にk8s-playgroundの開発を行えます。