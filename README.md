## Fork update
[![GoDoc](https://godoc.org/github.com/iyear/sysvc?status.svg)](https://godoc.org/github.com/iyear/sysvc)

This is a fork of the [kardianos/service](https://github.com/kardianos/service/). The original repository is no longer maintained actively. This fork is intended to fix bugs, refactor the code and merge upstream pull requests.

### Install

```bash
go get -u github.com/iyear/sysvc
```

### Changes
- [x] Move all scripts/configs to `.tmpl` files.
- [x] Add `ConfigPath()` function to `Service` interface to get the path of the configuration file. (On `Windows` it returns `ErrNoConfigPath` error)
- [x] Upgrade `golang.org/x/sys` to latest version.
- [ ] Support for listing all services on the system. 
- [ ] Write and run unit tests on GitHub Actions.
- [x] **[OpenRC]** Support for running as specific user.
- [x] **[Systemd]** Support custom `RestartSec` value. [Upstream#324](https://github.com/kardianos/service/pull/324)
- [x] **[Procd]** Support `Procd` init system used in OpenWRT. [Upstream#366](https://github.com/kardianos/service/pull/366)
- [x] **[Windows]** Reduce permission requirement to query service status.
[Upstream#402](https://github.com/kardianos/service/pull/402)
----

## service

service will install / un-install, start / stop, and run a program as a service (daemon).
Currently supports Windows XP+, Linux/(systemd | Upstart | SysV), and OSX/Launchd.

Windows controls services by setting up callbacks that is non-trivial. This
is very different then other systems. This package provides the same API
despite the substantial differences.
It also can be used to detect how a program is called, from an interactive
terminal or from a service manager.

## BUGS
 * Dependencies field is not implemented for Linux systems and Launchd.
 * OS X when running as a UserService Interactive will not be accurate.
