# 5gws

面向运营商固定内网源 IP 的轻量分流网关。客户端只需要把 DNS/DoT 指向网关；网关按域名和来源完成 DNS 分流，并接管内网来源的 TCP/80、TCP/443、UDP/443。

release 包只包含：

- `5gws`
- `config.example.toml`
- `rules.example.toml`

运行时配置由 `5gws` 生成：smartdns-rs、HAProxy、nftables、quicgw、systemd unit、可选 shadowsocks-rust。

## 策略

- 命中 CN 规则：走国内 DNS pool。
- 内网来源命中 GFW 规则：DNS A 查询返回 `gateway_ip`，再进入 HAProxy/quicgw。
- 内网来源未命中：走 `overseas_private` DNS pool。
- 非内网来源 DoT：走 `overseas_public` DNS pool，不返回网关 IP。
- TCP/QUIC 有 Host/SNI 但未命中规则：走 `routing.fallback_exit`。
- 缺 Host/SNI：拒绝，不做静默兜底。

nftables 只 redirect 同时匹配 `network.ingress_iface` 和 `network.internal_cidr` 的 80/443/53/853；非内网来源的默认 80/443 不受 5gws 影响。

## 安装

可选：如果 Android 私人 DNS 或公网 DoT 要使用域名，先在 DNS 服务商添加一条 A 记录：

- 子域示例：`dot.example.com`。
- A 记录指向 VPS 公网 IP。
- Cloudflare 使用“仅 DNS / 灰云”，不要开启橙云代理。
- 等 `dig +short dot.example.com` 返回 VPS IP 后继续。

一键安装并进入引导(需要root权限)：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash
```

固定版本：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash -s -- --version 0.1.1
```

手动安装：

```sh
tar xf 5gws-linux-amd64-0.1.1.tar.gz
sudo install -m 755 5gws /usr/local/bin/5gws
sudo 5gws install
sudo 5gws doctor
sudo 5gws apply
```

首次缺少 `/etc/5gws/config.toml` 或 `/etc/5gws/rules.toml` 时，`5gws install` 会进入引导：

- `gateway IP`：默认读取入口网卡 IPv4，失败时为 `10.0.0.1`。
- `carrier internal CIDR`：默认 `172.22.0.0/16`。
- `ingress interface`：默认来自 `ip route show default`。
- `enable Apple/iOS profile flow`：默认启用，自动生成 iOS 证书和描述文件下载服务。

显式安装运行时：

```sh
sudo 5gws install-smartdns --yes
sudo 5gws install-ssrust --yes
```

干净卸载：

```sh
sudo 5gws uninstall --purge --yes
```

本地预览：

```sh
5gws render --config ./config.example.toml --rules ./rules.example.toml --out ./rendered
5gws doctor --config ./config.example.toml --rules ./rules.example.toml
```

`cert-server`、`quicgw`、`bot` 主要用于调试；正常部署由 `5gws apply` 按配置自动生成 systemd 服务并启动。

## config.toml

最小配置：

```toml
[network]
gateway_ip = "10.0.0.1"
internal_cidr = "172.22.0.0/16"
ingress_iface = "eth0"

[routing]
fallback_exit = "direct"

[[exits]]
name = "direct"
type = "direct"
```

常用字段：

- `network.gateway_ip`：返回给内网客户端的网关 IP。
- `network.internal_cidr`：运营商内网来源段，默认 `172.22.0.0/16`。
- `network.ingress_iface`：接收运营商内网流量的网卡。
- `routing.fallback_exit`：TCP/QUIC 未命中显式 gateway 规则时使用的出口，默认 `direct`。
- `dns.backend_resolvers`：HAProxy 解析真实目标域名使用的 DNS，不能指向会返回 `gateway_ip` 的 rewrite 入口。

smartdns-rs 上游 DNS 默认已内置，可按需覆盖：

```toml
[dns]
upstreams_cn = ["https://223.5.5.5/dns-query", "223.5.5.5", "119.29.29.29"]
upstreams_overseas_private = ["22.22.22.22"]
upstreams_overseas_public = ["https://cloudflare-dns.com/dns-query", "https://dns.google/dns-query", "https://dns.quad9.net/dns-query", "1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "22.22.22.22"]
backend_resolvers = ["1.1.1.1:53", "1.0.0.1:53", "8.8.8.8:53", "8.8.4.4:53", "9.9.9.9:53", "22.22.22.22:53"]
```

默认 `upstreams_overseas_private` 只使用 `22.22.22.22`；`upstreams_overseas_public` 和 `backend_resolvers` 使用主流公共 DNS 与 `22.22.22.22`。

shadowsocks-rust 出口：

```sh
openssl rand -base64 16
```

```toml
[[exits]]
name = "ss1"
type = "shadowsocks-rust"
server = "198.51.100.10"
server_port = 8388
method = "2022-blake3-aes-128-gcm"
password = "PASTE_OPENSSL_OUTPUT_HERE"
username = "default"
listen_address = "127.0.0.1"
listen_port = 1080
tcp = true
udp = true
timeout_seconds = 300
```

说明：

- `listen_address/listen_port` 是本机 `sslocal` 监听地址。
- `tcp/udp` 默认都是 `true`。
- `timeout_seconds` 单位是秒，写入 shadowsocks-rust JSON 的 `timeout`。
- `method` 默认 `2022-blake3-aes-128-gcm`。
- 使用 SS2022 时，`2022-blake3-aes-128-gcm` 的 `password` 用 `openssl rand -base64 16` 生成。

## rules.toml

默认规则直接导入 MetaCubeX 的 sing-box source JSON：

```toml
[[imports]]
name = "cn"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.json"
dns_pool = "cn"

[[imports]]
name = "gfw"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json"
exit = "direct"
```

规则动作二选一：

- `dns_pool = "cn"`：DNS-only 规则，只影响 smartdns-rs 上游选择，不进入 HAProxy/quicgw。
- `exit = "direct"` 或 `exit = "ss1"`：gateway 规则，内网 DNS 返回 `gateway_ip`，流量进入对应出口。

手写规则：

```toml
[[rules]]
name = "openai"
exit = "ss1"
domain_suffix = ["openai.com", "chatgpt.com"]
```

导入 Mihomo / Clash rule-provider：

```toml
[[imports]]
name = "mihomo-openai"
type = "mihomo"
url = "https://example.com/openai.yaml"
exit = "ss1"
```

支持的导入：

- `type = "sing-box"`：source rule-set JSON，不直接消费二进制 `.srs`。
- `type = "mihomo"` / `"mimoho"` / `"clash"` / `"clash-meta"`：rule-provider YAML。

当前 smartdns-rs 渲染只等价支持 `domain` 和 `domain_suffix`。遇到 `domain_keyword`、`domain_regex`、`ip_cidr`、`rule_set` 会显式失败，避免静默丢规则。MetaCubeX 默认 `cn.json` 和 `gfw.json` 均为 `domain_suffix`，可直接使用。

如果要扩大到 `geolocation-!cn`：

```toml
[[imports]]
name = "geolocation-not-cn"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/geolocation-!cn.json"
exit = "direct"
```

注意：该 ruleset 当前包含 `domain_regex`，v1 会显式拒绝，默认不启用。

## 客户端 DNS 设置

5gws 的客户端侧只需要系统 DNS/DoT 配置，不需要安装代理客户端。

### 苹果设备

启用 `[ios]` 后生成证书、描述文件和二维码：

```sh
sudo 5gws ios-link --config /etc/5gws/config.toml
```

命令会输出：

- `cert`：CA 证书下载链接。
- `profile`：DoT 描述文件下载链接。
- `cert_qr`：CA 证书二维码。
- `profile_qr`：DoT 描述文件二维码。

终端会直接显示 CA 证书和描述文件二维码，方便调试时扫码。脚本或 Telegram 场景可使用 `--no-qr` 只输出链接：

```sh
sudo 5gws ios-link --config /etc/5gws/config.toml --no-qr
```

安装步骤：

1. iPhone / iPad 连接运营商内网。
2. 扫描 `cert_qr` 或用 Safari 打开 `cert`，安装 CA 证书。
3. 进入 `设置 -> 通用 -> 关于本机 -> 证书信任设置`，启用该 CA 的完全信任。
4. 扫描 `profile_qr` 或用 Safari 打开 `profile`，安装 DoT 描述文件。
5. 进入 `设置 -> 通用 -> VPN 与设备管理`，确认 `5gws DoT` 描述文件已安装。

`ios.base_url` 必须是手机能访问到的地址；内置证书服务只允许 loopback 和 `network.internal_cidr` 来源访问。正常安装后证书服务由 `5gws apply` 自动管理，不需要手动运行 `cert-server`。

### Android

Android 9+ 使用系统私人 DNS：

1. 确保网关 DoT 入口有可访问的主机名，例如 `dot.example.com`。
2. 进入 `设置 -> 网络和互联网 -> 私人 DNS`。
3. 选择 `指定的私人 DNS 服务商主机名`。
4. 填入 DoT 主机名，例如 `dot.example.com`，不要填 IP。
5. 保存后关闭再打开移动网络，确认 DNS 生效。

注意：

- Android 私人 DNS 会按主机名校验证书，推荐使用域名和受信任证书。
- 5gws 当前内置的 `ios-link` 生成的是面向 Apple 描述文件的本地 CA 和 `gateway_ip` 证书；stock Android 直接填 IP 或不信任该 CA 时可能无法建立 DoT。

## Telegram

```toml
[telegram]
enabled = true
bot_env = "/etc/5gws/bot.env"
allowed_users = ["123456789"]
```

`bot_env` 写入：

```text
BOT_TOKEN=...
```

生产环境建议填写 `allowed_users`。

## 验证

```sh
go test ./...
VERSION=0.1.1
make release VERSION="$VERSION"
tar tf "dist/5gws-linux-amd64-${VERSION}.tar.gz"
```

tar 包应只包含 `5gws`、`config.example.toml`、`rules.example.toml`。
