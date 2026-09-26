# 本地构建

## 前端构建

```bash
cd frontend
npm install
npm run build
```

构建输出位于 `frontend/dist`。

## 后端构建

```bash
cd backend
go vet ./...
go test -race ./...
go build -o ../build/eyvescloud .
```

`go test -race` 会并行运行全部单元测试（含 LXC/KVM 各模块与 API 层），推荐每次改动后执行。

如果要打包嵌入式 Web 面板，需要先把前端构建产物同步到后端嵌入目录。

## 一键构建

项目根目录提供了构建脚本：

```bash
bash build.sh
```

该脚本用于串联前端构建、静态资源同步和 Go 二进制构建。

默认目标为 Linux amd64。需要构建 ARM64 包时可以指定：

```bash
EYVESCLOUD_GOARCH=arm64 bash build.sh
```

需要同时构建 amd64 和 arm64 发布包时：

```bash
EYVESCLOUD_GOARCH=all bash build.sh
```

构建完成后会生成：

- `dist/eyvescloud-linux-amd64`
- `dist/eyvescloud-linux-amd64.tar.gz`
- `dist/eyvescloud-linux-arm64`
- `dist/eyvescloud-linux-arm64.tar.gz`

## 文档站构建

```bash
cd docs
npm install
npm run dev
npm run build
```

`npm run dev` 用于本地预览，`npm run build` 用于生成静态文档。
