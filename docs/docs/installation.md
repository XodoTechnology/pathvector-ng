---
sidebar_position: 2
---

# Installation

<!--
import {DownloadForm} from "../src/components/DownloadForm";

<DownloadForm/>
-->

All versions of Pathvector for all supported platforms are available to download from the
latest [release page](https://github.com/XodoTechnology/pathvector-ng/releases).

It is recommended to run Pathvector every 12 hours to update IRR prefix lists and PeeringDB prefix limits.
Adding `0 */12 * * * pathvector generate` to your crontab will update the filters at 12 AM and PM every day.

The only required dependency is `bird >= 2.0.7`, but some features require additional dependencies:

- RPKI filtering: RTR server. Pathvector NG defaults to the project's public RTR at `rtr.irrd3.net:3233`; you can run your own (e.g. [gortr](https://github.com/cloudflare/gortr)) and set `rtr-server`.
- IRR queries and PeeringDB lookups default to the project's public services (`irr.irrd3.net`, `https://pdb.irrd3.net/api`); override with `irr-server`/`peeringdb-url` if you run your own.
- IRR prefix list generation: [bgpq4](https://github.com/bgp/bgpq4)
- VRRP daemon: [keepalived](https://github.com/acassen/keepalived)

## Packages

Pathvector NG on Linux is available for amd64, aarch64, and mips64 as binaries and deb and rpm packages
from [releases](https://github.com/XodoTechnology/pathvector-ng/releases). The `pathvector-ng` package installs the
`pathvector` binary and `replaces`/`conflicts` the upstream `pathvector` package, so it is a drop-in replacement.

> Note: the upstream `repo.pathvector.io` package repository belonged to the original author and does not carry
> Pathvector NG builds. Install packages straight from the GitHub release assets.

## Debian (Ubuntu, etc.)

Download the `.deb` for your architecture from the
[releases](https://github.com/XodoTechnology/pathvector-ng/releases) page, then:

```shell
apt install ./pathvector-ng_*_Linux_amd64.deb
```

## CentOS

Download the `.rpm` for your architecture from the
[releases](https://github.com/XodoTechnology/pathvector-ng/releases) page, then:

```shell
yum install ./pathvector-ng-*-Linux-amd64.rpm
```

## VyOS

VyOS is based on Debian, see [Debian](#debian) for more information.

## TNSR

TNSR is based on Ubuntu, see [Debian](#debian) for more information. Past TNSR versions were based on CentOS, see [Netgate's FAQ](https://www.netgate.com/tnsr-centos-to-ubuntu-faq) for more information.

## FreeBSD

Pathvector is available as an amd64/aarch64 binary for FreeBSD from
the [releases](https://github.com/XodoTechnology/pathvector-ng/releases) page.

## Ubiquiti EdgeOS

Ubiquiti EdgeRouters are based on Debian. Pathvector supports the ER-8-XG, ER-12P, ER-12, ERPro-8, ER-8, ER-6P, ERPoe-5,
ER-4, and ERLite-3 routers based on the MIPS64 architecture. See [Debian](#debian) for more information.

Not Supported (MIPS1004Kc): ER-X, ER-X-SFP, ER-10X

## Arista EOS

Pathvector can run on Arista switches by installing a SWIX (Switch Extension) file
from [releases](https://github.com/XodoTechnology/pathvector-ng/releases). First, copy the `pathvector-ng-*-arista-amd64.swix` file
to `/mnt/flash/` on your switch.

From the EOS CLI:

1. Copy the extension to extensions: `copy flash:pathvector-ng-1.0.0-arista-amd64.swix extensions:`
2. Install the extension: `extension pathvector-ng-1.0.0-arista-amd64.swix`
3. Mark the extension to be installed on boot: `copy installed-extensions boot-extensions`
4. Add the pathvector alias: `alias pathvector "bash pathvector"`
5. Add Pathvector to scheduler (
   optional): `schedule pathvector interval 720 max-log-files 1 command pathvector -c /mnt/flash/pathvector.yml -o /mnt/flash/bird/ -s /run/bird.ctl`
6. Save changes: `wr mem`

## Juniper JunOS

Pathvector can run on Juniper devices by installing a signed JunOS extension package. First, download the Pathvector
Juniper release, either to your local machine and SCP it over to the Juniper router/switch, or pull it directly in
the `request system software` command by replacing the filename with the URL to the file.

Add the Pathvector extension provider:
`set system extensions providers pathvector license-type customer deployment-scope commercial`

Install the Pathvector package:
`request system software add pathvector-ng-juniper.tgz` or `request vmhost software add pathvector-ng-juniper.tgz`

## Cisco IOx

Pathvector can run on IOx compatible Cisco devices by installing a IOx package release, or directly to the device with
ioxclient. See https://developer.cisco.com/docs/iox/#!app-management/application-management for more information.

## Nokia Service Router (SR) Linux

Nokia SR Linux is based on CentOS, see [CentOS](#centos) for more information.

## Mikrotik RouterOS

Pathvector has experimental RouterOS support. Mikrotik has discontinued this feature. See the [RouterOS Container](https://help.mikrotik.com/docs/display/ROS/Container) reference for installation instructions
for the container package.

To build a Docker image for an alternate architecture:

```bash
docker build --output type=tar,dest=pathvector-mikrotik-arm64v8.tar -t pathvector-cron:arm64v8 --build-arg ARCH=arm64v8 -f ../vendorbuild/mikrotik/Dockerfile ..
```

## Building from source

Pathvector can be easily built from source for some, but not all, of
the [many supported Go platforms](https://github.com/golang/go/blob/master/src/go/build/syslist.go).

For example, to build Pathvector for M1 Macs:

```bash
git clone https://github.com/XodoTechnology/pathvector-ng && cd pathvector-ng
GOOS=darwin GOARCH=arm64 go build
```
