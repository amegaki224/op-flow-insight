# OP Flow Insight

[简体中文](README.md) | [English](README_EN.md) | [日本語](README_JA.md)

面向 ImmortalWrt 25.12.0 x86_64 的 LuCI 流量洞察插件。它在路由器本地累计各内网主机的上传、下载用量，约每 2 秒展示实时速率与活动连接，并使用离线 GitHub 公共数据集展示远端 IP 的国家/地区、ASN 和 0–100 风险证据分。

> 当前实现把“OP”解释为 OpenWrt 系平台。如果目标固件不是 OpenWrt，请保留 Go 后台与数据层，替换 `openwrt/rootfs` 下的平台集成即可。

## 能力

- 按 LAN IP 累计上传与下载字节，正常重启后继续累计。
- 通过 DHCP lease 与邻居表按 MAC 合并同一设备的 IPv4/IPv6；在线列表只保留当前
  在线主机，离线期间仍保留累计和历史记录。
- 主机连接详情提供独立 IPv4、IPv6 标签，并标明 LAN、链路本地、公网 IPv6 以及
  路由器自身的 LAN IPv6 地址。
- 保存按日记录，可按指定日、月、季度、年汇总并导出 UTF-8 TXT 清单。
- 当前累计量在路由器本地时间每月 1 日 00:00 重置；历史归档不会随月度重置删除。
- 实时主机速率、总速率、活动连接数和约 10 分钟趋势图。
- 展示每条当前连接的方向、协议、源 IP/端口、目标 IP/端口。
- 支持 IPv4、IPv6、普通出站和端口转发（DNAT）入站；自动识别 LAN
  接口上由运营商委派的公网 IPv6 前缀。
- 自动读取 dnsmasq DHCP lease，显示主机名和 MAC。
- 自动排除路由器自己的接口地址。
- 远端 IP 的国家/地区、ASN 与运营组织全部离线查询，不把用户访问的 IP 发给在线查询 API。
- 多来源风险证据和可解释的 0–100 分；只提示，不自动封禁。
- 单一、无 CGO 的 Go 二进制；使用 ImmortalWrt 25.12.0 官方 SDK 生成原生 APK v3 安装包。

## 界面预览

### 实时带宽趋势

![OP Flow Insight 实时带宽趋势](docs/screenshots/live-trend.png)

### 内网主机流量

![OP Flow Insight 内网主机流量](docs/screenshots/lan-hosts.png)

### 当前连接与 IP 归属

![OP Flow Insight 当前连接与 IP 归属](docs/screenshots/current-connections.png)

## 支持环境

- ImmortalWrt 25.12.0，目标 `x86/64`、软件包架构 `x86_64`。
- apk-tools 3.0.5。
- x86_64 / amd64。
- 内核 conntrack 与 `/proc/net/nf_conntrack`。
- LuCI、rpcd、jsonfilter、CA 证书。

软件流量分载或硬件流量分载会让大量后续报文绕过常规 conntrack 统计。若需要较准确的累计量，请在防火墙设置中关闭 flow offloading。该插件适合家庭/中小网络的可观测性与排查，不应作为运营商计费系统。

## 界面语言与语言包

从 `0.1.1-r6` 开始，界面以英语作为源码默认文字，并使用 LuCI `_()` 翻译接口。
主程序不强制安装所有语言：

- 只安装 `op-flow-insight` 时显示英语，不需要英语语言包。
- 简体中文安装可选包 `luci-i18n-op-flow-zh-cn`。
- 日语安装可选包 `luci-i18n-op-flow-ja`。

语言包必须与主程序使用相同版本。`r1` 至 `r5` 的页面是硬编码中文，不能通过安装
`r6` 及后续语言包翻译；要使用英语或日语界面，需要同时升级到当前版本主程序。若希望整个
LuCI 都显示日语，固件还需要 LuCI 基础日语包（通常为 `luci-i18n-base-ja`）。

## 安装

从构建产物取得 `op-flow-insight-<版本>-r<修订>.apk`，上传到路由器后安装。自行构建的包没有加入 OpenWrt 官方签名仓库，因此需要显式允许本地未受信任包：

```sh
apk add --allow-untrusted ./op-flow-insight-0.1.1-r8.apk
/etc/init.d/op-flow enable
/etc/init.d/op-flow restart
```

根据 LuCI 当前语言，再安装一个可选语言包：

```sh
# 简体中文
apk add --allow-untrusted ./luci-i18n-op-flow-zh-cn-0.1.1-r8.apk

# 日语
apk add --allow-untrusted ./luci-i18n-op-flow-ja-0.1.1-r8.apk
```

然后打开 LuCI 的“状态 → 流量洞察”。首次安装后可点击“更新数据集”，也可在 SSH 中同步执行：

```sh
op-flowd -config /etc/config/op-flow update-data
```

查看健康状态：

```sh
op-flowd -config /etc/config/op-flow ctl health
logread -e op-flow
```

### ImmortalWrt 24.10.x IPK

24.10.x 仍使用 opkg/IPK。上传 IPK 后安装：

```sh
opkg install ./op-flow-insight_0.1.1-r8_x86_64.ipk
# 可选：简体中文或日语，二选一
opkg install ./luci-i18n-op-flow-zh-cn_0.1.1-r8_all.ipk
# opkg install ./luci-i18n-op-flow-ja_0.1.1-r8_all.ipk
/etc/init.d/op-flow enable
/etc/init.d/op-flow restart
```

## 使用 ImmortalWrt 官方 SDK 编译 x86_64 APK

需要 Linux、Go 1.23+，以及 ImmortalWrt 25.12.0 x86/64 SDK。以解压后的 SDK 为例：

```sh
bash ./scripts/build-apk.sh /opt/immortalwrt-sdk-25.12.0-x86-64
```

SDK 下载地址：

```text
https://downloads.immortalwrt.org/releases/25.12.0/targets/x86/64/immortalwrt-sdk-25.12.0-x86-64_gcc-14.3.0_musl.Linux-x86_64.tar.zst
SHA-256: c228059aa1e58c3b3ae58ce8dcc7549fd08379d8e231daf80fcca15b677564cb
```

产物位于 `dist/`。GitHub Actions 支持手动触发，并会下载、校验上述 SDK，用 SDK 内置的 `apk mkpkg` 生成 APK v3，再用同一个 SDK 的 `apk adbdump` 检查包名和架构。

默认流水线已经锁定与你的路由器一致的 ImmortalWrt 25.12.0、x86/64、GCC 14.3.0、musl SDK。该插件不包含内核模块，后台程序也不依赖 CGO，因此不依赖路由器的内核模块 ABI；LuCI、rpcd、jsonfilter 等运行时依赖仍由 APK 正常检查。

IPK 流水线另外锁定 ImmortalWrt 24.10.6 x86/64 官方 SDK：

```sh
bash ./scripts/build-ipk.sh /opt/immortalwrt-sdk-24.10.6-x86-64
```

## 采集原理与累计语义

后台轮询 Linux conntrack 的原始方向与回复方向字节计数来计算实时速率，同时订阅 ctnetlink 的 destroy 事件取得每条连接最终计数。后者可补齐完全发生在两个轮询之间的短连接，以及最后一次轮询到连接关闭之间的字节：

- LAN 主机发起连接：原始方向字节算上传，回复方向字节算下载。
- 外部经 DNAT 进入 LAN：原始方向字节算下载，回复方向字节算上传。
- LAN 到 LAN 的流量默认忽略。
- 路由器自身的地址自动忽略。

后台保存每条活动连接的上一次字节基线，所以守护进程重启后不会把仍在进行的连接重复累计。累计状态默认每 5 分钟原子写入 `/etc/op-flow/state.json`，正常停止时也会立即保存。

状态文件从 r8 起保存按本地自然日划分的设备流量记录，并在查询时汇总为日、月、季度
或年。实时页面中的“本月累计”会在路由器本地时间每月 1 日 00:00 清零，但按日历史、
设备身份和活动连接基线会保留，因此跨月连接不会重复计数，离线设备再次上线后也会继续
归入原有记录。

如果内核或权限不允许订阅 conntrack destroy 事件，页面会明确警告并退回纯轮询模式，此时极短连接可能少计。异常断电仍可能损失上次落盘后的增量。

## 风险评分

评分只针对连接的远端公网 IP：

1. IPsum 的命中次数映射为 20、35、50、62、72、80、86、90 分。
2. 类别数据源的基准严重度：
   - Spamhaus DROP / EDROP：95
   - Feodo botnet/C2：90
   - Blocklist Project Malware：85
   - DShield recent attacker：70
   - Blocklist Project Abuse：65
3. 取最高严重度作为基准，每多一个独立来源增加 5 分，封顶 100。

等级为：0–19 低、20–39 留意、40–59 中、60–79 高、80–100 严重。0 分仅表示“当前加载的数据集中未观察到”，不表示已知安全。

数据来源、许可和使用限制见 [NOTICE.md](NOTICE.md)。

## 配置

配置文件为 `/etc/config/op-flow`。常用项：

| 选项 | 默认值 | 说明 |
|---|---:|---|
| `lan_cidr` | RFC1918 + `fd00::/8` | 可重复配置的 LAN CIDR |
| `poll_interval` | `2s` | 实时采集间隔，最小 500ms |
| `save_interval` | `5m` | 累计状态落盘间隔，最小 30s |
| `max_flows` | `500` | Web 页面最多返回的活动连接 |
| `auto_update` | `1` | 自动更新离线数据 |
| `update_interval` | `24h` | 数据更新周期 |

## 隐私与安全

- 服务不监听 TCP 端口，只监听权限为 `0600` 的本地 Unix Socket。
- Web 页面只能通过已登录 LuCI 会话与 rpcd ACL 访问。
- 不采集报文载荷、域名、URL、账号或内容。
- 数据更新使用 HTTPS；当前会校验响应格式、文件大小并原子替换。更新元数据中记录 ETag、时间和 SHA-256。

## 已知边界

- NAT 后只能展示 conntrack 中可见的端点；运营商 CGNAT 之外的转换不可见。
- IP 归属只是网络注册/路由层信息，不等于设备或个人的真实位置。
- 公开威胁情报可能误报、滞后或被污染，应结合本地日志复核。
- IP 地址可被 CDN、云服务和共享主机复用，风险分不能直接归因给当前访问者。
