# 5gws

面向运营商固定内网源 IP 场景的轻量 DNS/域名分流网关。

客户端只配置系统 DNS/DoT，不安装代理客户端。服务端根据来源网段和域名规则决定 DNS 返回值，并用 HAProxy、内置 TCP gateway 和 nftables 接管需要进入网关的流量。

release 包只包含：

- `5gws`
- `config.example.toml`
- `rules.example.toml`

## 工作模式

- CN 域名：走国内 DNS pool。
- DNS-only 规则：只影响解析上游，客户端直连真实解析结果。
- 内网来源命中 gateway 规则：DNS A 返回 `network.gateway_ip`，发往 `gateway_ip` 的流量进入网关出口。
- 内网来源未命中：走 `overseas_private` DNS pool，客户端直连真实解析结果。
- 非内网来源 DoT：走 `overseas_public` DNS pool，不返回网关 IP。
- 发往 `gateway_ip` 的 TCP/80、TCP/443：由 HAProxy 读取 Host/SNI 并选择出口，不解密 TLS。
- 发往 `gateway_ip` 的其它 TCP 端口：由通用 TCP gateway 转发，并读取 HTTP Host 或 TLS SNI 还原目标域名。
- 发往 `gateway_ip` 的 UDP/443：默认 reject，让 Android Speedtest 等 app 回落到 TCP/SNI 路径；需要 HTTP/3/QUIC 时显式设置 `network.quic_policy = "proxy"`。
- 公共加密 DNS：默认 reject，避免 App 内置 DoH 绕过 5gws DNS 策略。
- 缺 Host/SNI 的 TCP/QUIC 流量：拒绝并写日志，不做静默兜底。

核心组件：

| 组件 | 职责 |
|---|---|
| smartdns-rs | DNS/DoT 入口、上游分组、按规则返回网关 IP |
| HAProxy | TCP/80、TCP/443 透明转发 |
| quicgw | 通用 TCP gateway；可选 UDP/443 QUIC 转发 |
| nftables | 内网来源重定向和后端端口保护 |
| shadowsocks-rust | 可选出口 |
| systemd | 运行时管理 |
| Telegram bot | 可选管理入口 |

## 快速开始

### 1. 准备 DoT 域名

在 DNS 服务商添加 A 记录，例如：

- `dot.example.com -> VPS 公网 IP`
- Cloudflare 使用“仅 DNS / 灰云”
- 等 `dig +short dot.example.com` 返回 VPS IP 后继续

### 2. 安装

一键安装(要root权限)：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | bash
```

固定版本：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | bash -s -- --version 0.1.9
```

重新进入引导并覆盖生成的配置：

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | bash -s -- --reconfigure
```

更新到最新 release：

```sh
sudo 5gws update
```

固定版本更新：

```sh
sudo 5gws update --version 0.1.10
```

`5gws update` 会下载 release tarball 和同名 `.sha256`，校验通过后备份当前二进制、原子替换、重启服务并运行 health-check。失败会自动恢复旧二进制并再次检查；没有 `.sha256` 的旧 release 会拒绝更新。

`5gws install` 会询问：

- `gateway IP`：返回给内网客户端的网关 IP。
- `carrier internal CIDR`：运营商内网来源段，默认 `172.22.0.0/16`。
- `ingress interface`：接收运营商内网流量的网卡。
- `DoT domain`：Android 私人 DNS / iOS 描述文件使用的主机名。
- `enable Apple/iOS profile flow`：是否生成 iOS 描述文件下载服务。

已有配置时，引导会把当前值作为默认值，直接按 Enter 保留。

### 3. 配置客户端

- Android 9+：系统私人 DNS 填 `dns.dot_domain`，不要填 IP。
- iOS / iPadOS：使用安装完成后输出的二维码或 profile 链接安装 DoT 描述文件。

### 4. 验证

```sh
5gws doctor
5gws status
5gws logs --component haproxy --since 10m --lines 200
5gws logs -m haproxy -s 10m -n 200 -f
```

长参数使用 `--`，短参数使用 `-`。

## 安装与卸载

手动安装 release 包：

```sh
tar xf 5gws-linux-amd64-0.1.9.tar.gz
install -m 755 5gws /usr/local/bin/5gws
5gws install
5gws doctor
5gws apply
```

重新生成默认配置和规则：

```sh
5gws install --reconfigure
```

显式安装运行时：

```sh
5gws install-smartdns --yes
5gws install-ssrust --yes
```

干净卸载：

```sh
5gws uninstall --purge --yes
```

本地预览渲染结果：

```sh
5gws render --config ./config.example.toml --rules ./rules.example.toml --out ./rendered
5gws doctor --config ./config.example.toml --rules ./rules.example.toml
```

## 配置文件

默认路径：`/etc/5gws/config.toml`

最小配置：

```toml
[network]
gateway_ip = "10.0.0.1"
internal_cidr = "172.22.0.0/16"
ingress_iface = "eth0"
# tcp_redirect_port = 18082
# quic_policy = "reject"
# encrypted_dns_policy = "reject"

[routing]
fallback_exit = "direct"

[dns]
dot_domain = "dot.example.com"
cert_file = "/etc/5gws/certs/fullchain.pem"
key_file = "/etc/5gws/certs/privkey.pem"

[logging]
level = "info"
access = true

[[exits]]
name = "direct"
type = "direct"
```

常用字段：

- `network.gateway_ip`：返回给内网客户端的网关 IP。
- `network.internal_cidr`：运营商内网来源段。
- `network.ingress_iface`：接收运营商内网流量的网卡。
- `network.tcp_redirect_port`：发往 `gateway_ip` 的非保留 TCP 流量进入通用 TCP gateway 的本地端口，默认 `18082`。
- `network.quic_policy`：`reject` 或 `proxy`，默认 `reject`。
- `network.encrypted_dns_policy`：`reject` 或 `allow`，默认 `reject`；用于阻止客户端通过公共 DoH/DoT 绕过 5gws DNS rewrite。
- `routing.fallback_exit`：进入网关但未命中显式 gateway 规则时使用的出口，默认 `direct`。
- `dns.dot_domain`：Android 私人 DNS 和 iOS 描述文件使用的 DoT 主机名。
- `dns.backend_resolvers`：HAProxy / TCP gateway 解析真实目标域名使用的 DNS，不能指向会返回 `gateway_ip` 的 rewrite 入口。
- `logging.access`：是否输出 HAProxy access log，默认 `true`。

默认 DNS 上游已内置。CN 上游默认偏向在海外 VPS 上仍返回国内 CDN 的 resolver；需要自定义时可覆盖 `dns.upstreams_cn`、`dns.upstreams_overseas_private`、`dns.upstreams_overseas_public` 和 `dns.backend_resolvers`。

高级兼容项保留在 `config.example.toml` 中，例如显式固定端口 `[[tcp_proxies]]` / `[[udp_proxies]]`。它们默认不启用，正常 Speedtest/Ookla 路径应优先使用通用 TCP gateway。

### shadowsocks-rust 出口

生成 SS2022 密钥：

```sh
openssl rand -base64 16
```

配置示例：

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

## 规则文件

默认路径：`/etc/5gws/rules.toml`

规则动作二选一：

- `dns_pool = "cn"`：DNS-only 规则，只影响 smartdns-rs 上游选择。
- `exit = "direct"` 或 `exit = "ss1"`：gateway 规则，内网 DNS 返回 `gateway_ip`，流量进入对应出口。

同一域名重复命中时，靠前规则优先。手写 `[[rules]]` 先于 `[[imports]]` 处理，可用于覆盖导入规则。

手写规则：

```toml
[[rules]]
name = "openai"
exit = "ss1"
domain_suffix = ["openai.com", "chatgpt.com"]
```

导入 sing-box source rule-set：

```toml
[[imports]]
name = "gfw"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json"
exit = "direct"
```

导入 Mihomo / Clash rule-provider：

```toml
[[imports]]
name = "mihomo-openai"
type = "mihomo"
url = "https://example.com/openai.yaml"
exit = "ss1"
```

默认规则见 `rules.example.toml`，包含：

- `cn`：走国内 DNS pool。
- `speedtest`：Speedtest/Ookla 返回 `gateway_ip`，由 HAProxy 或通用 TCP gateway 转发。
- `gfw`、`ip-geo-detect`、`stun`：作为 gateway 规则处理。
- `ip-check`、`ippure-stun`：常见检测域名走 `direct`。

当前 smartdns-rs 渲染只等价支持 `domain` 和 `domain_suffix`。导入规则里的其它 matcher 会跳过并输出 warning；手写 `[[rules]]` 仍严格校验。

## 客户端

Android：

1. 确保 `dns.dot_domain` 已解析到 `network.gateway_ip`，且证书校验正常。
2. 进入 `设置 -> 网络和互联网 -> 私人 DNS`。
3. 选择 `指定的私人 DNS 服务商主机名`。
4. 填入 `dns.dot_domain`。
5. 保存后关闭再打开移动网络，确认 DNS 生效。

iOS / iPadOS：

```sh
5gws ios-link --config /etc/5gws/config.toml
```

用 Safari 打开输出的 profile 链接，或扫描终端二维码安装描述文件。脚本或 Telegram 场景可使用 `--no-qr` 只输出链接。

`ios.base_url` 必须是手机能访问到的地址。内置证书服务只允许 loopback 和 `network.internal_cidr` 来源访问，正常安装后由 `5gws apply` 自动管理。

## 运维命令

```sh
5gws doctor
5gws status
sudo 5gws update
5gws apply
5gws logs --component haproxy --since 10m --lines 200
5gws logs -m haproxy -s 10m -n 200 -f
5gws detect-cidr --seconds 30
5gws ios-link --config /etc/5gws/config.toml
5gws uninstall --purge --yes
```

- `doctor`：检查配置、规则和运行依赖。
- `status`：查看 systemd 服务状态。
- `update`：下载 release、校验 SHA256、备份替换并失败回滚。
- `apply`：重新渲染配置并重启运行服务。
- `logs`：查看 journald 日志；`-f` / `--follow` 持续跟随。
- `detect-cidr`：抓包观察客户端来源 IP。
- `ios-link`：输出 iOS 描述文件链接和终端二维码。

## Telegram 管理

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

生产环境建议填写 `allowed_users`。为空时 bot 会允许所有 Telegram 用户。

在 group / topic 中，bot 只响应显式 @ 它的消息，例如 `/status@your_bot` 或 `@your_bot /status`。

常用命令：

```text
/menu
/status
/doctor
/ios
/config
/rules
/rule_list
/rule_add <domain> <exit|pool:name>
/rule_del <name>
/logs
```

`/rule_add` 只写入 `rules.toml` 中的 Telegram managed block，不改手写规则和 imports。写入前会备份，写入后运行 `doctor` 校验；通过后 bot 会显示确认应用按钮。

## 注意事项

- DoT 域名必须解析到 `network.gateway_ip`，Android 私人 DNS 会按主机名校验证书。
- 5gws 使用 certbot 签发的公开证书，不使用自签 CA 作为 Android 主路径。
- `dns.backend_resolvers` 不能指向会返回 `gateway_ip` 的 rewrite 入口。
- nftables 会接管内网来源的 DNS/DoT；业务流量只在目标地址为 `network.gateway_ip` 时进入网关。
- 5gws 不直接监听公网默认 `0.0.0.0:80/443`；公网默认 80/443 不受影响。
- STUN 包没有 Host/SNI；5gws 不做任意 UDP 域名分流。
- 缺 Host/SNI 的 TCP/QUIC 流量会被拒绝，不做静默兜底。

## 验证发布包

```sh
go test ./...
VERSION=0.1.0
make release VERSION="$VERSION"
tar tf "dist/5gws-linux-amd64-${VERSION}.tar.gz"
cd dist && sha256sum -c "5gws-linux-amd64-${VERSION}.tar.gz.sha256"
```

tar 包应只包含 `5gws`、`config.example.toml`、`rules.example.toml`。
