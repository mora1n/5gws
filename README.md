# 5gws

面向固定内网源 IP 场景的 DNS 与域名分流网关。客户端只需配置系统 DNS 或 DoT，5gws 即可根据来源网段和域名选择直连或 Shadowsocks 出口，并接管需要经过网关的 TCP/QUIC 流量。

## 功能

- 提供 UDP/TCP DNS 与 DNS over TLS，支持规则缓存和多上游解析。
- 按域名将流量分配至 direct 或 shadowsocks-rust 出口。
- 提供 Vue 3 + DaisyUI Web 面板，用于配置、状态监控、日志和更新。
- 通过 Unix socket 管理本机 CLI，面板默认仅监听 localhost。
- 以单个 `5gws.service` 守护进程管理所有运行组件。

## 系统要求

- Linux amd64 与 systemd
- root 权限
- 一个已解析到服务器的 DoT 域名
- 首次申请 TLS 证书时 TCP/80 可从公网访问

## 安装

```sh
wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash
```

安装向导会询问以下内容：

| 配置项 | 用途 | 示例 |
|---|---|---|
| 网关 IPv4 | 分流域名返回给客户端的服务器地址 | `203.0.113.10` |
| 客户端网段 | 允许使用网关的客户端来源 CIDR | `172.22.0.0/16` |
| 入口网卡 | 接收客户端流量的服务器网络接口 | `eth0` |
| DoT 域名 | DNS over TLS 和面板使用的证书域名 | `dns.example.com` |

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
  --dot-domain dns.example.com
```

iOS 配置描述文件默认关闭，需要时在安装参数中添加 `--ios`。

## Nginx 反代

Web 面板仅监听 `https://127.0.0.1:19443`。将示例中的 `dns.example.com` 替换为安装时填写的 DoT 域名：

```nginx
location / {
    proxy_pass https://127.0.0.1:19443;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
    proxy_buffering off;
    proxy_read_timeout 1h;

    proxy_ssl_server_name on;
    proxy_ssl_name dns.example.com;
}
```

## 首次登录

安装完成后，从服务日志中获取一次性 setup token：

```sh
sudo journalctl -u 5gws.service -n 30 --no-pager
```

## 常用命令

```sh
sudo 5gws status
sudo 5gws doctor
sudo 5gws logs
sudo 5gws apply
sudo 5gws rollback REVISION_ID
sudo 5gws update
```

## 卸载

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

Release 包含静态二进制 `5gws-linux-amd64` 及其 SHA-256 校验文件。

## License

[MIT](LICENSE)
