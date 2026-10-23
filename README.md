# ShellBeam Server

[简体中文](README_zh-cn.md)

ShellBeam Server is the self-hosted monitoring backend for [ShellBeam](https://github.com/0xJean/shellbeam). It is an independent fork of [Komari](https://github.com/komari-monitor/komari), maintained by the ShellBeam project. It is not an official Komari release.

The server retains Komari's API, Agent protocol, account storage, monitoring history, and plugin interfaces. ShellBeam provides its own client. The embedded page identifies the service and points to the client; it does not provide a complete Web administration console. Builds do not download or bundle `komari-web`.

## Run from source

Install Go 1.25 or later and a C compiler for CGO SQLite, then run:

```sh
go build -o dist/shellbeam-server .
./dist/shellbeam-server server --listen 127.0.0.1:25774
```

For a build with an explicit ShellBeam version and the checkout's commit hash:

```sh
make build
./dist/shellbeam-server --version
```

The default primary database remains `./data/komari.db`. On a new instance, connect from the ShellBeam client, choose **Initialize a new service**, and supply an administrator account and a metrics database path on the server, such as `./data/metrics.db`. Existing initialized services use administrator sign-in or their existing API key.

## Run with Docker

```sh
docker build -t shellbeam-server:local .
docker compose up -d --build
```

The Compose example builds locally, persists `/app/data` in a named volume, and publishes port `25774` on host loopback. Configure your HTTPS reverse proxy or bind the published port to the interface needed by your clients and Agents. Use `/app/data/metrics.db` when initializing this container so metrics share the persistent volume. The container runs as UID/GID `10001`; bind-mounted data must be writable by that account.

A Git checkout supplies the build commit automatically. Building a source archive without `.git` requires `--build-arg VERSION_HASH=<commit>`.

## Compatibility and operation

- ShellBeam's product version is independent of the Komari API compatibility version. `/api/version` keeps `version: "1.6.0"`; build flags only set `utils.ServerVersion` and `utils.VersionHash`.
- The same read-only `/api/version` identity is available before first-run initialization and in recovery guides, so clients can check `product: "ShellBeam Server"` before submitting administrator credentials. Installation mode still exposes no normal account or monitoring routes.
- Existing Komari Agent executables remain usable. Server releases do not install, rename, or update Agents.
- `KOMARI_LISTEN` and the existing database flags remain supported. `SHELLBEAM_LISTEN` is a branded alias; an explicit `--listen` flag takes precedence.
- Existing data should first be tested on an offline backup with the same filesystem paths. Older databases may require the retained migration or recovery APIs; the service entry page does not perform these operations.

See [operation and migration](docs/shellbeam-server.md) for the initialization, authentication, persistence, and recovery boundaries. [UPSTREAM.md](UPSTREAM.md) records the exact upstream source and attribution.

## Development and releases

```sh
make check       # go test ./... and go vet ./...
make build       # dist/shellbeam-server, with product version and commit
make notices     # third-party license texts; requires Python 3
```

CI tests, vets, builds, and checks a local container image. The release workflow can build manually or from a `v*` version tag. It packages Linux amd64/arm64 binaries compiled with Go 1.25 on Debian Bookworm, checksums, MIT license, upstream NOTICE, and dependency license texts. Tag builds create a draft GitHub release; manual builds publish a draft only when requested. No workflow deploys a service or pushes container images.

Linux is the release and full-test target. On macOS, inherited JavaScript runtime tests include Linux-only process metrics and filesystem path assumptions; run the full suite in the container with `docker build --target check .`.

## License

[MIT](LICENSE). The original Komari copyright and [NOTICE](NOTICE) are retained. Distributed packages and containers include third-party license texts. See [upstream maintenance](docs/upstream.md) for the fork's source history and synchronization policy.
