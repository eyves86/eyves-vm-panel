# 发布流程

EyvesCloud 的安装和升级依赖 GitHub Release 产物。发布时建议使用语义化版本标签，例如 `v1.1.29`。

## 版本号

版本号需要同步检查：

- `backend/internal/version/version.go`
- `frontend/package.json`
- Release 标签。

## Release 产物

安装脚本会按宿主架构优先下载 Linux AMD64 或 ARM64 产物：

```text
eyvescloud-linux-amd64.tar.gz
eyvescloud-linux-arm64.tar.gz
```

在部分场景中也会尝试下载单独二进制：

```text
eyvescloud-linux-amd64
eyvescloud-linux-arm64
```

## 安装脚本行为

- `EYVESCLOUD_VERSION=latest`：使用 GitHub `releases/latest`。
- `EYVESCLOUD_VERSION=vX.Y.Z`：下载指定标签的 Release 产物。

示例：

```bash
EYVESCLOUD_VERSION=v1.1.29 sh install.sh
```

## 发布后验证

- 安装脚本可以下载新版本。
- `systemctl status eyvescloud` 正常。
- `/api/version` 返回新版本。
- Web 面板可以加载前端资源。
- 容器列表、任务队列、API Key 页面可以正常打开。
