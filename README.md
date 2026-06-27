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

一键安装并进入引导(需要root权限)：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | bash
```

固定版本：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | bash -s -- --version 0.1.0
```

手动安装：

```sh
tar xf 5gws-linux-amd64-0.1.0.tar.gz
sudo install -m 755 5gws /usr/local/bin/5gws
sudo 5gws install
sudo 5gws doctor
sudo 5gws apply
```

首次缺少 `/etc/5gws/config.toml` 或 `/etc/5gws/rules.toml` 时，`5gws install` 会进入引导：

- `gateway IP`：默认读取入口网卡 IPv4，失败时为 `10.0.0.1`。
- `carrier internal CIDR`：默认 `172.22.0.0/16`。
- `ingress interface`：默认来自 `ip route show default`。

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

说明：5gpn 的 `PRIVATE_OVERSEAS_DNS` 默认是 `1.1.1.1/8.8.8.8/9.9.9.9`；5gws 当前按项目决策将 `upstreams_overseas_private` 固定为 `22.22.22.22`。

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

## iOS 证书

启用 `[ios]` 后：

```sh
5gws ios-link --config /etc/5gws/config.toml
```

会生成 CA 证书、DoT mobileconfig、证书二维码和描述文件二维码。iPhone 可扫码或用 Safari 打开链接安装。

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
make release VERSION=0.1.0
tar tf dist/5gws-linux-amd64-${VERSION}.tar.gz
```

tar 包应只包含 `5gws`、`config.example.toml`、`rules.example.toml`。
