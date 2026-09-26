# Local Build

## Frontend Build

```bash
cd frontend
npm install
npm run build
```

The build output is in `frontend/dist`.

## Backend Build

```bash
cd backend
go vet ./...
go test -race ./...
go build -o ../build/eyvescloud .
```

`go test -race` runs all unit tests in parallel (including the LXC/KVM modules and the API layer); it is recommended to run it after every change.

To package the embedded web panel, first sync the frontend build output to the backend embed directory.

## One-shot Build

A build script is provided at the project root:

```bash
bash build.sh
```

It chains the frontend build, static asset sync, and Go binary build.

The default target is Linux amd64. To build an ARM64 package, specify:

```bash
EYVESCLOUD_GOARCH=arm64 bash build.sh
```

To build both amd64 and arm64 release packages:

```bash
EYVESCLOUD_GOARCH=all bash build.sh
```

After the build finishes, the following artifacts are generated:

- `dist/eyvescloud-linux-amd64`
- `dist/eyvescloud-linux-amd64.tar.gz`
- `dist/eyvescloud-linux-arm64`
- `dist/eyvescloud-linux-arm64.tar.gz`

## Docs Site Build

```bash
cd docs
npm install
npm run dev
npm run build
```

`npm run dev` previews the docs locally; `npm run build` generates the static documentation.
