# OP Flow Insight

[简体中文](README.md) | [English](README_EN.md) | [日本語](README_JA.md)

OP Flow Insight is a LuCI traffic observability plugin for ImmortalWrt 25.12.0
x86_64. It keeps cumulative upload and download counters for each LAN host,
shows live rates and active connections approximately every two seconds, and
uses offline public GitHub datasets to display the country/region, ASN, and an
explainable 0–100 risk score for remote IP addresses.

> In this project, “OP” refers to OpenWrt-family platforms. For another
> firmware platform, keep the Go daemon and data layer and replace the
> integration under `openwrt/rootfs`.

## Features

- Persistent upload and download byte counters per LAN IP.
- Merges a device's IPv4 and IPv6 addresses by MAC using DHCP leases and the
  neighbor table. Only online hosts occupy the live list; retained usage stays
  available while a host is offline.
- Separate IPv4 and IPv6 connection tabs, with LAN, link-local, global, and
  router-owned LAN IPv6 labels.
- Retained daily records with selectable day, month, quarter, and year
  aggregation plus UTF-8 TXT export.
- Live cumulative counters reset at 00:00 on the first day of each month in
  router local time, without deleting retained history.
- Live per-host and aggregate rates, active connection counts, and a roughly
  ten-minute trend chart.
- Direction, protocol, source IP/port, and destination IP/port for each active
  connection.
- IPv4, IPv6, ordinary outbound traffic, and inbound port forwarding (DNAT),
  including automatic discovery of ISP-delegated global IPv6 prefixes on LAN.
- Host names and MAC addresses read automatically from dnsmasq DHCP leases.
- Automatic exclusion of the router's own interface addresses.
- Fully offline country/region, ASN, and network organization lookup for remote
  IPs; visited IPs are not sent to an online lookup API.
- Multi-source threat evidence and an explainable 0–100 score. The plugin
  reports risk but never blocks an IP automatically.
- A single CGO-free Go binary. The native APK v3 package is produced with the
  official ImmortalWrt 25.12.0 SDK.

## Screenshots

### Live bandwidth trend

![OP Flow Insight live bandwidth trend](docs/screenshots/live-trend.png)

### LAN host traffic

![OP Flow Insight LAN host traffic](docs/screenshots/lan-hosts.png)

### Current connections and IP attribution

![OP Flow Insight current connections and IP attribution](docs/screenshots/current-connections.png)

## Supported environment

- ImmortalWrt 25.12.0, target `x86/64`, package architecture `x86_64`.
- apk-tools 3.0.5.
- x86_64 / amd64.
- Kernel conntrack and `/proc/net/nf_conntrack`.
- LuCI, rpcd, jsonfilter, and CA certificates.

Software or hardware flow offloading may cause later packets in a connection
to bypass ordinary conntrack accounting. Disable flow offloading in the
firewall settings when more accurate cumulative counters are required. This
plugin is intended for observability and troubleshooting in home and
small/medium networks; it is not a carrier-grade billing system.

## UI language and language packages

Starting with `0.1.1-r6`, English is the source and fallback UI language and
all interface strings use LuCI's `_()` translation API. The core package does
not force every translation to be installed:

- The `op-flow-insight` package alone displays English; no English language
  package is needed.
- Install `luci-i18n-op-flow-zh-cn` for Simplified Chinese.
- Install `luci-i18n-op-flow-ja` for Japanese.

The translation package must match the core package version. Releases `r1`
through `r5` contain hard-coded Chinese strings and cannot be translated by an
`r6` or later language package; upgrade the core package to the current version for English or
Japanese. To display all of LuCI in Japanese, the firmware also needs the LuCI
base Japanese translation package, usually `luci-i18n-base-ja`.

## Installation

Download `op-flow-insight-<version>-r<revision>.apk`, upload it to the router,
and install it. Locally built packages are not signed by the official OpenWrt
repository, so installation must explicitly allow an untrusted local package:

```sh
apk add --allow-untrusted ./op-flow-insight-0.1.1-r8.apk
/etc/init.d/op-flow enable
/etc/init.d/op-flow restart
```

Install one optional translation package for the selected LuCI language:

```sh
# Simplified Chinese
apk add --allow-untrusted ./luci-i18n-op-flow-zh-cn-0.1.1-r8.apk

# Japanese
apk add --allow-untrusted ./luci-i18n-op-flow-ja-0.1.1-r8.apk
```

Open **Status → Flow Insight** in LuCI. After the first installation, click
**Update datasets**, or run the update over SSH:

```sh
op-flowd -config /etc/config/op-flow update-data
```

Check daemon health and logs:

```sh
op-flowd -config /etc/config/op-flow ctl health
logread -e op-flow
```

### ImmortalWrt 24.10.x IPK

ImmortalWrt 24.10.x still uses opkg/IPK:

```sh
opkg install ./op-flow-insight_0.1.1-r8_x86_64.ipk
# Optional: choose Simplified Chinese or Japanese
opkg install ./luci-i18n-op-flow-zh-cn_0.1.1-r8_all.ipk
# opkg install ./luci-i18n-op-flow-ja_0.1.1-r8_all.ipk
/etc/init.d/op-flow enable
/etc/init.d/op-flow restart
```

## Building the x86_64 APK with the official SDK

Linux, Go 1.23+, and the ImmortalWrt 25.12.0 x86/64 SDK are required:

```sh
bash ./scripts/build-apk.sh /opt/immortalwrt-sdk-25.12.0-x86-64
```

SDK:

```text
https://downloads.immortalwrt.org/releases/25.12.0/targets/x86/64/immortalwrt-sdk-25.12.0-x86-64_gcc-14.3.0_musl.Linux-x86_64.tar.zst
SHA-256: c228059aa1e58c3b3ae58ce8dcc7549fd08379d8e231daf80fcca15b677564cb
```

Artifacts are written to `dist/`. GitHub Actions can be triggered manually and
downloads and verifies the same SDK, builds the native APK v3 with the SDK's
`apk mkpkg`, and checks the resulting name and architecture with `apk adbdump`.

The default pipeline is pinned to the same ImmortalWrt 25.12.0 x86/64,
GCC 14.3.0, musl SDK as the target router. The daemon contains no kernel module
and does not use CGO, so it has no router kernel-module ABI dependency. Normal
runtime dependencies such as LuCI, rpcd, and jsonfilter are still enforced by
the package manager.

The IPK pipeline is pinned separately to the official ImmortalWrt 24.10.6
x86/64 SDK:

```sh
bash ./scripts/build-ipk.sh /opt/immortalwrt-sdk-24.10.6-x86-64
```

## Accounting model

The daemon polls the original and reply byte counters from Linux conntrack to
calculate live rates. It also subscribes to ctnetlink destroy events to obtain
the final counters for each connection. Destroy events account for short
connections that occur entirely between two polling cycles and for bytes
transferred between the final poll and connection teardown.

- Connection initiated by a LAN host: original-direction bytes are upload and
  reply-direction bytes are download.
- External connection DNATed to a LAN host: original-direction bytes are
  download and reply-direction bytes are upload.
- LAN-to-LAN traffic is ignored by default.
- The router's own addresses are excluded automatically.

The daemon stores the last observed counters for every active connection so a
restart does not count an existing connection twice. Cumulative state is
written atomically to `/etc/op-flow/state.json` every five minutes by default
and is also saved on a normal shutdown.

Starting with r8, the state file also retains traffic per device and local
calendar day. Day, month, quarter, and year views are aggregated at query time.
The live “this month” counters reset at 00:00 on the first day of each month in
router local time, while daily history, device identity, and active-connection
baselines are preserved. A connection spanning midnight is therefore not
counted twice, and an offline device resumes its existing record when it returns.

If the kernel or permissions do not allow conntrack destroy subscriptions, the
UI displays a warning and falls back to polling only. Very short connections
may then be undercounted. An unexpected power loss can also lose increments
since the last state write.

## Risk scoring

Scores apply only to the remote public IP of a connection:

1. IPsum hit counts map to 20, 35, 50, 62, 72, 80, 86, and 90.
2. Category dataset base severities:
   - Spamhaus DROP / EDROP: 95
   - Feodo botnet/C2: 90
   - Blocklist Project Malware: 85
   - DShield recent attacker: 70
   - Blocklist Project Abuse: 65
3. The highest severity is used as the base score. Each additional independent
   source adds 5 points, capped at 100.

Bands are 0–19 low, 20–39 guarded, 40–59 medium, 60–79 high, and 80–100
critical. A score of 0 only means that the IP was not observed in the currently
loaded datasets; it does not mean that the IP is known to be safe.

See [NOTICE.md](NOTICE.md) for data sources, licenses, and usage restrictions.

## Configuration

The configuration file is `/etc/config/op-flow`.

| Option | Default | Description |
|---|---:|---|
| `lan_cidr` | RFC1918 + `fd00::/8` | Repeatable LAN CIDR |
| `poll_interval` | `2s` | Live sampling interval; minimum 500 ms |
| `save_interval` | `5m` | State persistence interval; minimum 30 s |
| `max_flows` | `500` | Maximum active connections returned to the web UI |
| `auto_update` | `1` | Automatically update offline datasets |
| `update_interval` | `24h` | Dataset update interval |

## Privacy and security

- The service does not listen on a TCP port. It only uses a local Unix socket
  with mode `0600`.
- The web UI is accessible only through an authenticated LuCI session and its
  rpcd ACL.
- Packet payloads, domain names, URLs, account names, and content are not
  collected.
- Dataset updates use HTTPS. Response format and size are validated before
  atomic replacement, and update metadata records the ETag, timestamp, and
  SHA-256.

## Known limitations

- Behind NAT, only endpoints visible to conntrack can be shown; translation
  beyond a carrier-grade NAT is not visible.
- IP geolocation represents network registration and routing information, not
  the physical location of a device or person.
- Public threat intelligence can contain false positives, delays, or poisoned
  data. Confirm findings against local logs.
- CDN, cloud, and shared-hosting addresses are reused. A risk score must not be
  attributed directly to the current visitor.
