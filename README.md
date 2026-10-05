# 5gws

5gws 是面向固定客户端网段的 DNS 与域名分流网关。指定客户端网段配置系统 DNS 或 DNS over TLS 后，5gws 根据域名规则选择直连或 Shadowsocks 出口，并把需要经过网关的 TCP/QUIC 流量接入对应出口。普通 Wi-Fi 的公网 DoT 使用真实公网解析，同一私人 DNS 域名可在两种网络间切换。

## 功能

- 提供 UDP/TCP DNS 与 DNS over TLS。
- 按域名规则选择 direct 或 shadowsocks-rust 出口。
- Web 面板支持规则、出口、DNS 上游、日志和更新。
- 支持为特定域名规则选择独立 DNS 池。
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

- 在“规则”中查看当前已应用规则，并编辑当前配置。
- 在“出口”中添加或修改 Shadowsocks 出口。
- 规则和出口名称支持 emoji、空格及其他 Unicode 字符。
- 自定义规则和导入可在“规则”中调整顺序，越靠前越先匹配，并默认优先于默认规则。
- 在“DNS 与网络”中配置网关地址、客户端网段、DNS 上游、默认出口和策略。
- “DNS 与网络”支持新增、改名和删除自定义 DNS 池；池改名时会同步更新当前规则引用。
- “预检”只验证当前页面配置，不修改运行状态；“应用”会再次预检并原子生效。
- 配置没有变化时，“应用”不会重启受管进程。
- 应用期间数据面可能短暂重连，面板会自动等待并显示最终结果。
- 在“日志”中实时查看运行状态和错误，支持搜索、暂停跟随和下载。

默认国内池包含阿里 DNS 等国内上游。首次安装或从未包含自定义 DNS 池的旧版本升级时，还会创建两组普通自定义池和本地规则：`cn_netease` / `netease-music` 避免网易云音乐命中不可用的竞速结果，`cn_unicom` / `china-unicom-app` 为中国联通 App 选择稳定的国内运营商 CDN。它们不是只读系统规则，可以在面板中编辑、重命名或删除。

## DNS 入口与网络切换

指定客户端网段的 DoT 流量由 nftables 转入网关入口，使用域名分流、网关 IPv4 重写和自定义 DNS 池，并抑制 AAAA、SVCB、HTTPS 记录。公网 DoT 使用 `overseas_public` 池，不执行地址重写或记录类型抑制，适用于普通 Wi-Fi。公网池应只配置可返回真实公网地址的上游；`22.22.22.22` 保留在内网海外池，不能用于普通 Wi-Fi。

SmartDNS 0.13.0 与 0.13.1 的磁盘缓存格式不同。升级后若日志出现旧缓存加载错误，应先备份 `/var/log/smartdns/smartdns.cache`，将旧文件移出缓存路径，再重启服务重新生成；规则和数据库不受影响。

运行概览分别显示 UDP DNS 和 DoT 指标；DoT 健康状态同时检查网关与公网入口。`5gws doctor` 会实际探测 DNS 上游、两个 DoT 入口、证书和出口，并展示错误。若网络切换后仍无法访问，请记录故障时间并对照这两项状态，手机切换行为需用真实设备验证。

## 证书续期

安装会创建仅处理当前 DoT 域名的 Certbot deploy hook。续期成功后，钩子验证证书域名、有效期和私钥，再更新 DoT 证书副本；安装了 Nginx 的服务器会验证配置并重新加载 Nginx，然后重启 `5gws.service`，数据面会短暂重连。无效证书不会覆盖现有副本，部署错误会明确返回给 Certbot。

已有安装可补装钩子：

```sh
sudo 5gws deploy-certificate --install-hook
```

首次安装使用 Certbot standalone。已经用 Nginx 监听 80 的服务器应配置 ACME webroot，并通过 `certbot reconfigure --cert-name <DoT 域名> --webroot -w <webroot>` 修改续期方式，再执行 `certbot renew --cert-name <DoT 域名> --dry-run` 验证。普通卸载会移除 5gws 创建的部署钩子。

## 常用命令

```sh
sudo 5gws status
sudo 5gws doctor
sudo 5gws logs
sudo 5gws reset-admin
sudo 5gws update
```

停止服务后可用 `sudo 5gws compact` 回收 SQLite 中已清理 revision 占用的文件空间。

卸载：

```sh
sudo 5gws uninstall --yes
```

普通卸载会停止并移除 `5gws.service`，但保留数据库、出口、规则、管理员账号和其他配置。之后再次运行 `5gws install` 并输入相同的安装参数时，会自动复用这份配置，不会重置管理员密码。

如果要执行全新安装并删除所有 5gws 状态（包括数据库、生成状态、配置和证书），请显式使用：

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
