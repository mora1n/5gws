# 5gws

面向运营商固定内网源 IP 场景的 DNS/域名分流网关。客户端只配置系统 DNS 或 DoT；服务端根据来源网段和域名决定 DNS 返回值，并接管需要进入网关的 TCP/QUIC 流量。

## 架构

系统只有一个 systemd unit：`5gws.service`。

`5gws daemon` 负责：

- 提供嵌入式 Vue 3 + DaisyUI 管理面板和 REST API。
- 通过 `/run/5gws/control.sock` 接收本机 CLI 请求。
- 监督 smartdns-rs、HAProxy 和按需启动的 sslocal 子进程。
- 运行通用 TCP gateway、可选 QUIC gateway、iOS profile 和指标采集。
- 用 SQLite 管理配置草稿、活动版本、历史、账号、会话、指标和远程规则缓存。

数据面保持精简：

| 组件 | 职责 |
|---|---|
| smartdns-rs | UDP/TCP DNS、DoT、domain-set、缓存和上游竞速 |
| HAProxy | TCP/80 Host 与 TCP/443 SNI 分流 |
| 5gws gateway | 其它 TCP、原目标恢复和可选 QUIC |
| shadowsocks-rust | 可选 Shadowsocks 出口 |
| nftables | DNS/网关流量重定向和后端端口保护 |

HAProxy 最大连接数默认是 `16384`，可在面板的“DNS 与网络”页调整；设为 `0` 时交由 HAProxy 自动推导。默认值为高并发保留充足余量，同时避免高 `LimitNOFILE` 让 HAProxy 为数十万空闲连接预分配内存。

SQLite 是唯一运行时配置来源。TOML 只用于面板中的显式备份导入/导出，不存在运行时 `config.toml` 或 `rules.toml`。

## 安装

仅支持全新安装的 Linux amd64 主机。安装器检测到旧数据库或旧 `5gws-*.service` 时会拒绝覆盖。

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash
```

固定版本：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash -s -- --version 0.2.0
```

安装向导收集 gateway IP、运营商内网 CIDR、入口网卡、DoT 域名和 iOS 开关；随后安装运行时依赖、申请证书、初始化 `/var/lib/5gws/5gws.db` 并启动唯一的 `5gws.service`。

首次启动的一次性 setup token：

```sh
sudo journalctl -u 5gws.service -n 30 --no-pager
```

面板默认监听 `https://<DoT 域名>:8443`，只接受 loopback、运营商内网 CIDR 和面板中配置的额外管理 CIDR。

## 运维

CLI 必须以 root 访问权限为 `0600` 的 Unix socket：

```sh
sudo 5gws status
sudo 5gws doctor
sudo 5gws logs
sudo 5gws apply
sudo 5gws rollback REVISION_ID
sudo 5gws ios-link
```

面板提供：

- 运行状态、进程、内存、连接、流量和 DNS 探针指标。
- DNS、网络、规则导入、direct/SS 出口和 iOS 配置。
- 草稿保存、完整预检、显式应用、失败回滚和历史版本回滚。
- 实时日志、诊断、TOML 备份和带 SHA-256 校验的自更新。

配置应用流程固定为：解析与校验、并发刷新规则源、编译路由、渲染 revision、`smartdns test`、`haproxy -c`、`nft -c`、切换运行态、readiness probe、提交 active revision。任一步失败都会保留或恢复旧活动版本，并返回原始错误。

远程规则支持 sing-box JSON 和 Mihomo rule-provider。服务端使用 ETag/Last-Modified 条件请求；只有明确的 `304 Not Modified` 才复用缓存，网络错误不会使用陈旧内容继续应用。

## 卸载

```sh
sudo 5gws uninstall --purge --yes
```

## 构建

```sh
cd web && corepack pnpm install --frozen-lockfile
make test
make build VERSION=dev
make release VERSION=0.2.0
```

`make build` 先构建并嵌入面板，再以 `CGO_ENABLED=0` 生成单一静态二进制。release 资产只有 `5gws-linux-amd64` 和对应的校验元数据 `5gws-linux-amd64.sha256`。
