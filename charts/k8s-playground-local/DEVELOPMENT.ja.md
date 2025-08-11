# Local Development Chart - Differences from Public Chart

このディレクトリ (`charts/k8s-playground-local/`) には、ローカル開発専用のHelmチャートが含まれています。
このチャートは [公開されているk8s-playground-chart](https://github.com/tyottodekiru/k8s-playground-chart) をベースにしていますが、ローカル開発のために以下の重要な変更が加えられています。

## 🔧 主な変更点

### 1. バイナリマウント対応（ホットリロード）
**変更箇所**: 各コントローラーのDeployment/StatefulSetテンプレート

**実装**:
- **hostPathボリューム**: kindクラスター内でホストの `/mnt/bin` ディレクトリを共有
- **軽量Alpine**: アプリケーション本体は含めず、最小限の実行環境のみ提供
- **実行時マウント**: コンテナ起動時に最新のローカルビルドバイナリを使用

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

**実装**:
- **hostPathマウント**: `/mnt/web` から `/app/web` への直接マウント

### 3. NFS サーバーのローカル開発対応
**変更箇所**: `templates/nfs-server-statefulset.yaml`

**実装**:
- **tmpfs使用**: メモリ上での高速動作、Pod再起動時のクリーンな状態
- **symlink戦略**: `/exports` → `/exports-tmpfs` で既存コードとの互換性確保
- **lifecycle hooks**: Pod起動時の自動symlink作成

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

**実装**:
- **パスワード認証への切替**: `admin` / `admin123` で即座にログイン可能

### 5. リソース制限の調整
**変更箇所**: 各コントローラーのリソース設定

**実装**:
- **大幅なリソース削減**: memory 128Mi～512Mi、CPU 100m～500m程度

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

**実装**:
- **`pullPolicy: "Never"`**: レジストリアクセスを完全に無効化
- **オフライン開発**: インターネット接続なしでも開発可能

### 7. 名前空間の統一
**変更箇所**: `dev/values.yaml`

**実装**:
- **`k8s-playground` 名前空間**: 開発専用の独立した環境
- **リソース管理の簡素化**: `kubectl delete namespace k8s-playground` で全削除可能

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