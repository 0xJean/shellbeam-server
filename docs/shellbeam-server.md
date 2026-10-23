# Operating ShellBeam Server

ShellBeam Server supplies the backend. The embedded HTML page provides service identification and a ShellBeam client entry point. It has no account editor, Agent installer, database migration wizard, or complete Web administration console. The existing backend APIs remain available to compatible clients and administrative tooling.

## Build and startup

Use Go 1.25 or later with CGO enabled and a C compiler. `go build -o dist/shellbeam-server .` builds directly; Node.js, a frontend repository, and downloaded Web assets are not required. `make build` adds the ShellBeam product version and Git commit using linker flags. Set `SERVER_VERSION` and `VERSION_HASH` explicitly for a build from a source archive.

```sh
make build SERVER_VERSION=0.1.0-dev
./dist/shellbeam-server server --listen 127.0.0.1:25774 --database ./data/komari.db
```

The default service port is `25774`; the default listen address is `0.0.0.0:25774`. An explicit `--listen` wins over `SHELLBEAM_LISTEN`, then `KOMARI_LISTEN`, then the default. The default primary database path remains `./data/komari.db`, relative to the process working directory. Preserve the working directory when replacing an existing service.

The Docker build compiles with CGO on Debian Bookworm and uses a matching Debian Bookworm runtime. It does not require a prebuilt binary. Ordinary builds retain `.git` in the build context to resolve the exact commit; the final image contains the executable and legal texts, not the checkout. An archive without Git metadata needs an explicit `VERSION_HASH` build argument. Release archives target Linux amd64/arm64 with glibc 2.36 or later.

## First initialization and authorization

1. Start the service with an empty, writable data directory. In the ShellBeam client, add its endpoint and choose **Initialize a new service**.
2. Supply the first administrator username/password and a metrics database path on the server. For the Compose container, use `/app/data/metrics.db`.
3. The client reads `/api/install/status` and sends `/api/install/complete` only when `required` is true. Installation is not replayed on an initialized instance.
4. After startup finishes, the client signs in via `/api/login`, verifies the compatible server version, reuses an existing server API key or creates one only when none exists, verifies `/api/admin/client/list`, and stores the key in the client's Vault.

The initial install API creates the first administrator without a prior session. Keep a new instance on a controlled endpoint until initialization completes. Administrator passwords are not part of the server build or Compose file.

For an initialized instance, use administrator sign-in, including `2fa_code` if enabled, or an existing server API key. The backend retains `session_token` cookie authentication and `Authorization: Bearer <key>` authentication. The compatibility routes and API key semantics do not change with the product name.

## Persistent data and existing deployments

The primary SQLite database contains accounts, API keys, client identities/configuration, and metric-store configuration. Metrics live in a separate configured store. Plugins, runtime files, and other writable resources can also be under the working directory's `data/` tree. Back up all of them, not only `komari.db`.

For an existing Komari deployment:

1. Record the upstream version, service working directory, command-line flags, environment, and configured metrics DSN. Check the source baseline in [UPSTREAM.md](../UPSTREAM.md).
2. Stop the existing process and make a complete offline copy of its primary database, metrics store, and data directory. Preserve SQLite `-wal`/`-shm` files if present. For an external store, take its corresponding consistent backup.
3. Test ShellBeam Server against copies in an isolated location. A metrics DSN stored inside the copied primary database may still point at the original store: isolate or update it through the retained recovery API before allowing writes. Never run both services against the same writable databases.
4. Keep UUIDs, Agent tokens, API keys, and existing paths. If normal startup and client verification succeed, switch the service executable while preserving its working directory/configuration and the endpoint used by Agents.

A database migration can modify schemas and history. Compatibility with the source baseline does not guarantee arbitrary older/newer database versions can start without a migration. Verify migrations against copies first. To roll back after a migration, restore the matching complete backup with the previous executable; do not point an older binary at a migrated database.

The Compose named volume persists `/app/data` across container replacement. `docker compose down` preserves it; `docker compose down --volumes` removes it. A bind mount replacing the named volume must be writable by container UID/GID `10001`. Changing `--database` or the metrics path outside `/app/data` requires another persistent mount.

## Migration and recovery boundaries

The retained startup lifecycle can enter database migration or metric-store recovery mode. In those modes normal monitoring APIs may be unavailable until the required operation completes. The ShellBeam service entry page does not perform the operation.

Use a compatible client or administrative API tooling against the existing authenticated `/api/admin/database-migration` and `/api/admin/database-recovery` APIs. These are backend interfaces, not a promise that the current ShellBeam client implements every legacy migration or recovery screen. Inspect server logs and the API status before retrying a failed startup. Preserve the backup if the client does not support the required mode.

## Version and Agent compatibility

The existing `/api/version` success envelope has these `data` fields. Clients can keep their existing compatibility checks and use the added fields for product identification:

| Field | Meaning |
| --- | --- |
| `version` | Komari compatibility level, `1.6.0` |
| `hash` | Actual ShellBeam checkout commit, or `unknown` for a plain development build |
| `product` | `ShellBeam Server` |
| `product_version` | ShellBeam release version, initially `0.1.0-dev` |
| `upstream_version` | Pinned upstream release, `1.6.0` |
| `upstream_commit` | `1b76b7ad5bc3de6529bb7f0577069b4ac241fec3` |

The legacy `/api/version` `version` field remains the Komari API compatibility version `1.6.0`. ShellBeam Server has a separate product release version. Build tooling only injects the product version and actual fork commit; it does not replace the API compatibility value with a product release tag.

Existing Agent binaries continue using the original HTTP/WebSocket/RPC paths, token formats, and report shapes. This repository builds the server, not Agent executables. Keep Agent installation, signing, update, and rollback policy in the existing ShellBeam Agent distribution workflow.

Plugin APIs and manifest compatibility remain part of the backend contract. The client currently checks an exact compatible upstream version for its managed load-alert extension. When updating upstream, validate the client and plugin together before changing the compatibility field.

## Build checks and distribution

The full runtime test target is Linux. On macOS, the inherited suite includes Linux-only process metrics and `/var` versus `/private/var` path assumptions. A successful native build alone is not evidence that all runtime tests passed; use the container `check` stage for the full suite.

`make check` runs `go test ./...` and `go vet ./...`; `make build` creates the binary. The compatibility smoke accepts an absolute executable path:

```sh
python3 scripts/compatibility-smoke.py --binary "$PWD/dist/shellbeam-server"
docker build --target check .
```

The container `check` stage runs Go tests/vet on Linux with the same toolchain as the image. `make notices` requires Python 3 and collects Go dependency license texts into `dist/THIRD_PARTY_NOTICES/`. Module license directories use an `_modules/` prefix so subsequent Go package traversal skips them.

CI has read-only repository permissions. Release builds run tests/vet and compile native Linux amd64/arm64 packages in Bookworm containers. Pushing a `v0.1.0`-style tag creates a draft GitHub release. Manual runs can build development artifacts without a tag; publishing a draft requires an existing validated version tag and the explicit publish input. The workflow refuses to overwrite an existing release. Review and publish the draft separately. No workflow deploys or pushes a container image.

Every release archive includes `LICENSE`, `NOTICE`, `UPSTREAM.md`, operation documents, and `THIRD_PARTY_NOTICES/`. Images carry the legal files under `/usr/share/doc/shellbeam-server/`.
