# 5gws

5gws 是面向固定客户端网段的 DNS 与域名分流网关。客户端配置系统 DNS 或 DNS over TLS 后，5gws 根据域名规则选择直连或 Shadowsocks 出口，并把需要经过网关的 TCP/QUIC 流量接入对应出口。

## 功能

- 提供 UDP/TCP DNS 与 DNS over TLS。
- 按域名规则选择 direct 或 shadowsocks-rust 出口。
- Web 面板支持规则、出口、DNS 上游、日志和更新。
- 可选生成 iOS DNS over TLS Profile 和安装二维码。
- 面板默认只监听本机 HTTP，由 Nginx 负责公网 HTTPS。
- `5gws.service` 统一管理后台运行组件。

## 系统要求

- Linux amd64 与 systemd
- root 权限
- 一个已解析到服务器的 DoT 域名
- 首次申请证书时，公网 TCP/80 需要能访问到服务器

## 安装

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash
```

安装向导会询问：

| 配置项 | 用途 | 示例 |
|---|---|---|
| 网关 IPv4 | 分流域名返回给客户端的服务器地址 | `203.0.113.10` |
| 客户端网段 | 允许使用网关的客户端来源 CIDR | `172.22.0.0/16` |
| 入口网卡 | 接收客户端流量的服务器网卡 | `eth0` |
| DoT 域名 | DNS over TLS 使用的域名 | `dns.example.com` |

安装指定版本：

```sh
sudo bash install.sh --version <version>
```

无交互安装：

```sh
sudo bash install.sh -- \
  --non-interactive \
  --gateway-ip 203.0.113.10 \
  --internal-cidr 172.22.0.0/16 \
  --ingress-iface eth0 \
  --dot-domain dns.example.com \
  --panel-listen 127.0.0.1:19443
```

`--panel-listen` 可修改 Web 后端监听地址；默认是 `127.0.0.1:19443`。

安装时增加 `--ios` 可启用 iOS Profile。公开地址默认使用 `https://<DoT 域名>`，不单独开放 HTTP 端口。

## 首次登录

首次安装完成后，terminal 会显示管理员账号和随机密码：

```text
Username: admin
Password: <随机密码>
```

如果需要重置管理员密码，在服务器上运行：

```sh
sudo 5gws reset-admin
```

## Nginx 反代

Web 后端默认只监听本机 HTTP。公网 HTTPS 交给 Nginx 处理：

```nginx
location / {
    proxy_pass http://127.0.0.1:19443;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
    proxy_buffering off;
    proxy_read_timeout 1h;
}
```

## iOS Profile

启用并应用 iOS Profile 后，“设置”页面会显示二维码和下载链接：

```text
https://dns.example.com/ios/5gws-dot.mobileconfig
```

Profile 和二维码由 Web 后端通过 `/ios/` 提供，HTTPS 仍由上面的 Nginx 反代处理。无需监听或防火墙放行 `8088`。

## 面板使用

- 在“规则”中查看当前已应用规则，并编辑草稿规则。
- 在“出口”中添加或修改 Shadowsocks 出口。
- 在“DNS 与网络”中配置网关地址、客户端网段、DNS 上游、默认出口和策略。
- 修改后先点“保存”，需要正式生效时再点“应用”。
- 在“日志”中查看运行状态和错误。

## 常用命令

```sh
sudo 5gws status
sudo 5gws doctor
sudo 5gws logs
sudo 5gws reset-admin
sudo 5gws update
```

卸载：

```sh
sudo 5gws uninstall --purge --yes
```

## 构建

```sh
cd web
corepack pnpm install --frozen-lockfile
cd ..
make test
make build VERSION=dev
make release VERSION=<version>
```

Release 包含静态二进制和 SHA-256 校验文件。

## License

[MIT](LICENSE)
