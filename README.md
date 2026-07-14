# ros2pi

Run ROS 2 on a Raspberry Pi — including its GPIO, I2C, SPI and serial hardware —
without installing ROS on the Pi.

> **Status: early development. Not usable yet.**
> Only `ros2pi check --dump-facts` exists today, and it prints JSON. There is no
> working `run`, `build` or `shell` yet. See [Status](#status) for what is real
> and what is not. Watch or star if you want to know when it becomes useful.

## Why this exists

Running ROS 2 on a Pi means running it in a container. That is not a workaround —
it is the upstream recommendation. Raspberry Pi OS is Debian, which
[REP 2000](https://reps.openrobotics.org/rep-2000/) rates **Tier 3** ("community
reports indicate that the release is functional. The development team does not
run the unit test suite or perform any other tests"). `arm64` Ubuntu containers
are **Tier 1**. ROS 2's own installation guide for the Raspberry Pi says as much.

So you reach for Docker, and then you hit the real problem: the container needs
to be told about your Pi, and getting that wrong fails in ways that actively
mislead you.

- Your workspace fills with **root-owned files** you can't edit, because the ROS
  image runs as root and `--user` was omitted.
- A device is **listed in the container and `open()` still returns EPERM**,
  because the Pi's `gpio`/`i2c`/`spi` groups are allocated dynamically and don't
  exist inside the image, so `--group-add gpio` silently grants nothing.
- `i2cdetect` finds **nothing on a bus that exists**, because `dtparam=i2c_arm`
  was never enabled — a firmware setting no container can fix.
- Your code targets `gpiochip4` per a Pi 5 tutorial and breaks, because
  **kernel 6.6.47 moved it back to `gpiochip0`**.

The usual advice is to paste `--privileged` and move on. That works, and it means
your robot's nodes run with full access to the host.

ros2pi's plan is to probe the host, work out what is actually true about *your*
Pi, and construct the right `docker` invocation — including the fine-grained
device and group flags that make `--privileged` unnecessary.

## Status

| Milestone | What | State |
|---|---|---|
| **M0** | Host probing (`HostFacts`) | **done** — this is what's in the repo |
| M1 | `init` / `build` / `shell` + `ros2` passthrough | not started |
| M2 | `check` — human-readable diagnosis | not started |
| M3 | GPIO / I2C / SPI / UART / USB passthrough | not started |
| M4 | Release binaries, install script | not started |

M0 is the foundation the rest rests on: everything the tool will do is a function
of what it can learn about the host, so that came first. 64 tests, no hardware
required to run them.

## Requirements

- A Raspberry Pi 3, 4, 5, Zero 2 W, or CM3/4/5
- **64-bit** Raspberry Pi OS (Debian 12 or 13)
- Docker

### arm64 only — and that is not our choice

32-bit Raspberry Pi OS cannot run ROS 2 in a container, regardless of this tool:

- The official ROS 2 images publish **`amd64` and `arm64` only**. There is no
  `armv7` manifest. Docker Hub's `arm32v7/ros` says so itself: *"WARNING: THIS
  IMAGE IS NOT SUPPORTED ON THE arm32v7 ARCHITECTURE"*.
- **Docker Engine v29 dropped `armhf` packages for Raspberry Pi OS.** 32-bit
  Trixie isn't listed on Docker's install page at all.
- Per REP 2000, Debian `arm32` is Tier 3 and source-build only.

If you're on 32-bit Pi OS and your board is a Pi 3 or newer, reflashing with the
64-bit image is the fix. Pi 1 and Pi Zero/Zero W are ARMv6 and cannot run 64-bit
at all.

## Try it

There are no releases yet. From source:

```bash
git clone https://github.com/artineering/ros2pi.git
cd ros2pi
go build ./cmd/ros2pi
./ros2pi check --dump-facts
```

Needs Go 1.25+. If your `apt` Go is older, the Go toolchain downloads the right
version automatically — no action needed.

You get a JSON description of your Pi: model and family, GPIO chips and their
labels, I2C/SPI/serial nodes, group GIDs, firmware `config.txt` state, and how
Docker is (or isn't) working. It is not pretty. Making it pretty is M2.

## Help wanted: send us your Pi

The hardest problem with a tool like this is that we can only test it on the
hardware we own. So `--dump-facts` exists to fix that:

```bash
./ros2pi check --dump-facts > my-pi.json
```

**Please open an issue and attach that file** — especially if you have a **Pi 5**,
a Compute Module, or anything unusual. Every fixture we receive becomes a
permanent regression test, and a maintainer can replay your exact host:

```bash
ROS2PI_FACTS=my-pi.json ros2pi check --dump-facts
```

It contains a hardware description — model, kernel, device nodes, group IDs, your
local username. No keys or secrets. Read it before you post it.

**Pi 5 support is written but has never run on a real Pi 5.** It is modelled from
documentation and tested against synthetic fixtures. If you have one, you are the
person who can tell us whether we got it right.

## Design notes

A few decisions worth knowing if you read the code:

**Probing is separated from interpretation.** `internal/hostfacts` is the only
package that touches the machine; it produces a serialisable `HostFacts`.
Everything downstream is a pure function of that value. This is what lets a Pi 5
on kernel 6.6 be tested on a Pi 4, or in CI with no Pi at all.

**Device numbers are never hardcoded**, because they are kernel-assigned
artefacts rather than properties of the hardware. GPIO chips are identified by
*label* through the chardev ioctl (`pinctrl-bcm2711` = Pi 4, `pinctrl-rp1` =
Pi 5), so the 6.6.47 renumbering is a non-event. The I2C header bus is resolved
through the device-tree alias to its controller node — the presence of some
`/dev/i2c-N` proves nothing, since a Pi 4 with I2C disabled still exposes
`i2c-0/10/20/21/22` for HDMI DDC.

**Unknown hardware warns instead of guessing.** An unrecognised chip label
disables GPIO and asks for a bug report. A wrong guess produces a confidently
incorrect flag that fails later with a message you cannot act on.

**Failures are classified, not just detected.** "You are not in the `docker`
group" and "you are, but this shell predates it" produce identical output from
Docker and need opposite fixes — one wants `usermod`, the other wants you to log
out and back in, and neither wants `sudo`. Comparing `/etc/group` against
`getgroups(2)` tells them apart.

## Prior art

[`rocker`](https://github.com/osrf/rocker) (OSRF) solves the general problem of
injecting user, X11 and device flags into `docker run` for ROS, and is worth your
attention if you're on an Ubuntu workstation. ros2pi is narrower: Raspberry Pi
only, and concerned with the Pi's own hardware quirks.

## Not affiliated with Open Robotics

This is an independent project. It is **not affiliated with, endorsed by, or
supported by** Open Robotics / the Open Source Robotics Foundation. ROS is a
trademark of the Open Source Robotics Foundation. Please report bugs here, not to
the ROS project.

## License

Not yet chosen. Until one is added, no rights are granted beyond viewing the
source — that will be fixed before any release.
