# ShellBeam Server

[English](README.md)

ShellBeam Server 是 [ShellBeam](https://github.com/0xJean/shellbeam) 的自托管监控后端，由 ShellBeam 项目独立维护，基于 [Komari](https://github.com/komari-monitor/komari) fork。它不是 Komari 官方发行版。

服务端保留 Komari API、Agent 探针协议、账户存储、监控历史和插件接口。前端由 ShellBeam 客户端提供；内嵌页面只介绍服务及客户端入口，不提供完整的 Web 管理后台。构建不下载或打包 `komari-web`。

## 从源码运行

准备 Go 1.25 或更新版本和支持 CGO SQLite 的 C 编译器：

```sh
go build -o dist/shellbeam-server .
./dist/shellbeam-server server --listen 127.0.0.1:25774
```

需要写入 ShellBeam 产品版本及当前提交标识时：

```sh
make build
./dist/shellbeam-server --version
```

主数据库默认路径仍为 `./data/komari.db`。新服务启动后，在 ShellBeam 客户端连接该地址，选择“初始化新服务”，设置首位管理员和服务器上的指标数据库路径，例如 `./data/metrics.db`。已初始化的服务使用管理员登录或现有 API 密钥接入。

## 使用 Docker

```sh
docker build -t shellbeam-server:local .
docker compose up -d --build
```

Compose 示例在本地构建镜像，将 `/app/data` 保存到命名卷，并把 `25774` 端口绑定到宿主机回环地址。为远程客户端和 Agent 配置 HTTPS 反向代理，或按需修改宿主机端口绑定。初始化容器时使用 `/app/data/metrics.db`，确保指标也写入持久卷。容器以 UID/GID `10001` 运行；使用宿主机目录挂载时，该账户需要写权限。

从 Git checkout 构建会自动记录真实提交。没有 `.git` 的源码归档需要传入 `--build-arg VERSION_HASH=<commit>`。

## 兼容性和运维

- ShellBeam 产品版本与 Komari API 兼容版本分别管理。`/api/version` 保留 `version: "1.6.0"`；构建参数只覆盖 `utils.ServerVersion` 和 `utils.VersionHash`。
- 保持既有 Komari Agent 可用。服务端发行流程不安装、改名或升级 Agent。
- 保留 `KOMARI_LISTEN` 和原有数据库参数。`SHELLBEAM_LISTEN` 是品牌别名；显式 `--listen` 优先。
- 迁移已有数据时，先使用离线备份，在相同路径下验证。旧数据库可能需要保留的迁移或恢复 API；服务入口页不执行这些操作。

初始化、鉴权、持久化、迁移和恢复边界见[运维说明](docs/shellbeam-server.md)。精确上游基线和归属见 [UPSTREAM.md](UPSTREAM.md)。

## 开发和发行

```sh
make check       # go test ./... 和 go vet ./...
make build       # 构建 dist/shellbeam-server，写入产品版本和提交
make notices     # 收集第三方许可证，需要 Python 3
```

CI 运行测试、vet、二进制构建与本地容器构建检查。独立发行工作流支持手动触发及 `v*` 版本标签，使用 Go 1.25 和 Debian Bookworm 构建 Linux amd64/arm64 制品，附带校验和、MIT 许可证、上游 NOTICE 和依赖许可证。标签构建创建 GitHub Release 草稿；手动构建仅在明确勾选发布时创建草稿。工作流不部署服务，不推送容器镜像。

## 许可证

采用 [MIT](LICENSE)，保留 Komari 原作者版权及 [NOTICE](NOTICE)。发行包及容器包含第三方许可文本。上游历史和同步约定见[维护说明](docs/upstream.md)。
