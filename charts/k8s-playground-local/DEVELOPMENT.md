# Local Development Chart - Differences from Public Chart

This directory (`charts/k8s-playground-local/`) contains a Helm chart specifically designed for local development.
While based on the [public k8s-playground-chart](https://github.com/tyottodekiru/k8s-playground-chart), it includes several critical modifications for optimal local development experience.

## 🔧 Key Modifications

### 1. Binary Mounting (Hot Reload)
**Modified Files**: All controller Deployment/StatefulSet templates

**Implementation**:
- **hostPath volumes**: Share host's `/mnt/bin` directory within kind cluster
- **Lightweight Alpine**: Provide minimal execution environment without application binaries
- **Runtime mounting**: Use latest locally-built binaries at container startup

```yaml
# Example: app-controller
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

### 2. Web Assets Mounting
**Modified Files**: app-controller template

**Implementation**:
- **hostPath mounting**: Direct mount from `/mnt/web` to `/app/web`

### 3. NFS Server Local Development Support
**Modified Files**: `templates/nfs-server-statefulset.yaml`

**Implementation**:
- **tmpfs usage**: Fast memory-based operation with clean state on Pod restart
- **symlink strategy**: `/exports` → `/exports-tmpfs` for existing code compatibility
- **lifecycle hooks**: Automatic symlink creation at Pod startup

```yaml
# Local development additional configuration
env:
- name: SHARED_DIRECTORY
  value: "/exports-tmpfs"  # Use tmpfs
lifecycle:
  postStart:
    exec:
      command: ["/bin/sh", "-c", "rm -rf /exports && ln -sf /exports-tmpfs /exports || true"]
```

### 4. Authentication Method Change
**Modified Files**: `dev/values.yaml`

**Implementation**:
- **Switch to password auth**: `admin` / `admin123` for immediate login

### 5. Resource Limit Adjustments
**Modified Files**: All controller resource configurations

**Implementation**:
- **Significant resource reduction**: memory 128Mi～512Mi, CPU 100m～500m

```yaml
resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"  # Greatly reduced from public version
    cpu: "500m"
```

### 6. Image Pull Policy
**Modified Files**: All controllers

**Implementation**:
- **`pullPolicy: "Never"`**: Completely disable registry access
- **Offline development**: Enables development without internet connectivity

### 7. Namespace Unification
**Modified Files**: `dev/values.yaml`

**Implementation**:
- **`k8s-playground` namespace**: Dedicated development environment isolation
- **Simplified resource management**: Complete cleanup with `kubectl delete namespace k8s-playground`

## 📂 File Structure Differences

```
charts/k8s-playground-local/
├── Chart.yaml                    # Identical to public chart
├── values.yaml                   # Identical to public chart (default values)
├── templates/                    # Following files are modified
│   ├── deployment-app-controller.yaml      # Binary mounting support
│   ├── deployment-collector-controller.yaml
│   ├── deployment-generator-controller.yaml
│   ├── deployment-killer-controller.yaml
│   ├── deployment-logging-controller.yaml
│   ├── nfs-server-statefulset.yaml        # tmpfs + symlink support
│   └── service.yaml                       # port-forward support
├── DEVELOPMENT.md                # This file (English)
└── DEVELOPMENT.ja.md            # Japanese version
```

## 🚀 Usage

1. **Development Environment Setup**:
   ```bash
   make dev
   ```

2. **Application Access**:
   ```bash
   make port-forward
   # Access http://localhost:8080 in browser
   # Login: admin / admin123
   ```

3. **Applying Code Changes**:
   ```bash
   make build    # Only rebuild Go binaries
   # No container restart or image rebuild required
   ```

## ⚠️ Important Notes

1. **DO NOT use in production**: This chart is development-only
2. **Security considerations**: Uses fixed passwords and privileged containers
3. **Data persistence**: NFS uses tmpfs, data is lost on Pod restart
4. **Application implementation is immutable**: Only Helm chart level modifications, no application code changes

## 🔄 Synchronization with Public Chart

When the public chart is updated:
1. Sync `values.yaml` and `Chart.yaml` with public version
2. Sync `templates/` files while preserving the above modifications
3. Add similar development support for new features

---

This local development chart enables efficient k8s-playground development for contributors.

## 📖 Documentation

- **English**: [DEVELOPMENT.md](DEVELOPMENT.md) (this file)
- **日本語**: [DEVELOPMENT.ja.md](DEVELOPMENT.ja.md)