# ros2pi

[![build](https://github.com/artineering/ros2pi/actions/workflows/build.yml/badge.svg)](https://github.com/artineering/ros2pi/actions/workflows/build.yml)

Run ROS 2 on a Raspberry Pi — including its GPIO, I2C, SPI and serial hardware —
without installing ROS on the Pi. It works out what your Pi actually is and
writes the `docker` command for you, so your nodes don't need `--privileged`.

> **Early, but the core works.** Building and running ROS 2 packages is verified
> on a real Pi 4, and `ros2pi check` tells you what your Pi needs and how to fix
> it. There are no releases yet, and it has only ever run on one person's
> hardware — see [Status](#status) for what is proven and what is not.

## Requirements

- A Raspberry Pi 3, 4, 5, Zero 2 W, or CM3/4/5
- **64-bit** Raspberry Pi OS (Debian 12 or 13)
- Docker
- Go 1.25+ to build from source. On Debian 13, `sudo apt install golang-go` gives
  1.24 and Go fetches the 1.25 toolchain it needs by itself — nothing else to do.
  Debian 12's `apt` Go is 1.19, too old to do that (the mechanism needs 1.21+),
  so take Go from [go.dev/dl](https://go.dev/dl/) or `bookworm-backports`.

<details>
<summary><b>Why 64-bit only</b></summary>

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

</details>

## Try it

There are no releases yet. Build from source:

```bash
git clone https://github.com/artineering/ros2pi.git
cd ros2pi
go build -o ~/.local/bin/ros2pi ./cmd/ros2pi
```

If `ros2pi` isn't found afterwards, `~/.local/bin` isn't on your `PATH` — add it,
or run the binary by its full path.

Then make a workspace and run a node in it:

```bash
mkdir -p ~/my_project && cd ~/my_project
ros2pi init

ros2pi pkg create --build-type ament_python --node-name my_node my_pkg
ros2pi build
ros2pi run my_pkg my_node        # -> Hi from my_pkg.
```

The first run pulls `ros:jazzy` (about 1.3 GB) and takes a few minutes. After
that the container stays up, so commands are quick.

## How you work

If you already know ROS 2, the fastest way to explain ros2pi is to put the two
side by side. Same project, same result:

<table>
<tr>
<th>Plain ROS 2, on a workstation</th>
<th>ros2pi, on the Pi</th>
</tr>
<tr>
<td>

```bash
# needs ROS 2 installed on the host

mkdir -p ~/ws/src && cd ~/ws
source /opt/ros/jazzy/setup.bash

ros2 pkg create --build-type ament_python \
  --node-name my_node my_pkg
# writes ./my_pkg, not ./src/my_pkg

# declare deps in package.xml, then:
sudo rosdep init        # first time only
rosdep update
rosdep install --from-paths src \
  --ignore-src -y
# installs onto the host, for good

colcon build --symlink-install
source install/setup.bash

ros2 run my_pkg my_node
```

</td>
<td>

```bash
# needs only Docker on the Pi

mkdir -p ~/my_project && cd ~/my_project
ros2pi init

ros2pi pkg create --build-type ament_python \
  --node-name my_node my_pkg
# writes ./src/my_pkg

# declare deps in package.xml, then:
ros2pi image build
# runs the same rosdep install, inside
# an image build -- nothing touches the Pi



ros2pi build


ros2pi run my_pkg my_node
```

</td>
</tr>
</table>

Same commands, same layout. What the table can't show is where your code lives
and what the loop feels like.

`ros2pi init` gives you an ordinary ROS 2 workspace — the same layout colcon and
every tutorial expect:

```
~/my_project/
  ros2pi.toml            which ROS image to use, and which hardware you want
  src/                   your packages live here
  build/ install/ log/   colcon's output (init gitignores these for you)
```

Your code stays on the Pi, in your own home directory. The workspace is
bind-mounted into the container at `/ros2_ws`, and the container runs as *you*
rather than root, so everything under `src/` stays editable from your normal
editor. Nothing you write lives inside the container.

Then it's edit, build, run:

```bash
# edit src/my_pkg/my_pkg/my_node.py in whatever editor you like
ros2pi build
ros2pi run my_pkg my_node
```

Because `ros2pi build` passes `--symlink-install`, editing an existing Python
file doesn't need a rebuild — just `ros2pi run` again and your change is live.
Rebuild when you add a new file, change `setup.py` or `package.xml`, or write
C++.

Anything `ros2` understands is passed through untouched:

```bash
ros2pi topic list
ros2pi launch my_pkg my_launch.py
ros2pi node info /my_node
```

Every command lands in the same container, so a second terminal can inspect a
node while it runs. (The node `pkg create` generates isn't one — it prints `Hi
from my_pkg.` and exits. That's the template, not a fault.)

`ros2pi down` stops the container; `ros2pi shell` drops you inside it. Add
`--dry-run` to any command to see what it would do without doing it.

`build/`, `install/` and `log/` sit on the Pi but hold binaries linked against
the *container's* ROS. They will not run on the host, which is why `init`
gitignores them.

### Adding dependencies

Declare them in `package.xml` as usual, then:

```bash
ros2pi image build
```

That reads every `package.xml` under `src/`, lets rosdep resolve them, and bakes
them into an image for this workspace. This avoids losing your dependencies when
the container is recreated, which is what happens if you install them into a
running container instead.

Only the `package.xml` files go into the image layer, not your source. So editing
your nodes doesn't reinstall dependencies, and changing a dependency does.

### When something is wrong

```bash
ros2pi check
```

It reports on your Pi and, for anything broken, tells you what to run. It works
without a workspace and without a working Docker, which is when you need it most.

```
Hardware
  ok    gpio chip   gpiochip0 [pinctrl-bcm2711] 58 lines, via ioctl
  FAIL  i2c         not enabled
        the header's controller is /soc/i2c@7e804000, per the device-tree alias
        it has no live bus, so the kernel never brought it up
        dtparam=i2c_arm is unset in /boot/firmware/config.txt

        Note: /dev/i2c-0, /dev/i2c-10, /dev/i2c-20 exist, but they are HDMI/DDC
        and camera buses, not the header. `ls /dev/i2c-*` looking healthy means
        nothing.
        fix: enable it:
              sudo raspi-config nonint do_i2c 0
              sudo reboot
  ok    groups      mapped
        gpio     -> --group-add 986 (numeric: not in the ROS image)
        dialout  -> --group-add dialout (by name: 20 matches the image)
```

`--json` for scripts, `--explain <id>` for one check, `--strict` to fail on
warnings too. `--dump-facts` still prints the raw JSON, which is what to attach
to a bug report — it describes your hardware, including your local username, so
read it before you post it.

## Status

| What | State |
|---|---|
| Reading the Pi's hardware | **works** — verified on a Pi 4 |
| `init` / `up` / `down` / `build` / `shell` | **works** — verified on a Pi 4 |
| Passing commands to `ros2` | **works** — verified on a Pi 4 |
| GPIO access | **works** — see below for exactly what that means |
| `ros2pi check` report | **works** — readable diagnosis with a fix for every problem |
| `ros2pi image build` | **works** — bakes package.xml deps into an image so they survive |
| I2C / SPI | code exists; the *refusal* path is verified, the working path is not |
| UART / USB serial | code exists, never run |
| Camera | not started |
| Releases / install script | not started |

105 tests, none of which need a Pi or Docker to run. CI builds and tests on real
arm64 hardware for every commit.

### What "GPIO works" means, precisely

A container started by ros2pi can open `/dev/gpiochip0` and read it, running as
an ordinary non-root user, with no `--privileged`:

```
running as: ubuntu uid=1000 groups=1000 986
OPENED gpiochip0 [pinctrl-bcm2711] 58 lines
```

That is the part everyone else gets wrong — group 986 is this Pi's `gpio` group,
which does not exist inside the ROS image, so it has to be passed as a number.
Getting it wrong is why so much advice on the internet says to use
`--privileged`.

What has **not** been proven: actually toggling a pin. Nothing has been wired to
this Pi. If you have an LED and five minutes, that is the single most useful
thing you could contribute.

## Why this exists

Running ROS 2 on a Pi means running it in a container. That is not a workaround —
it is the upstream recommendation, because Raspberry Pi OS is Debian (a Tier 3
ROS platform) while `arm64` Ubuntu containers are Tier 1.

The hard part is telling the container about your Pi, and getting it wrong fails
in ways that actively mislead you: root-owned files you can't edit, devices that
are listed in the container but refuse to open, buses that look healthy but were
never enabled. The usual advice is to paste `--privileged` and move on. It works,
but it gives every node full access to the host, so any bug in any node can reach
the whole machine.

ros2pi probes the host, works out what is actually true about *your* Pi, and
constructs the right `docker` invocation — including the fine-grained device and
group flags that make `--privileged` unnecessary.

<details>
<summary><b>The specifics, if you've hit this before</b></summary>

[REP 2000](https://reps.openrobotics.org/rep-2000/) rates Debian **Tier 3**
("community reports indicate that the release is functional. The development team
does not run the unit test suite or perform any other tests"). `arm64` Ubuntu
containers are **Tier 1**. ROS 2's own installation guide for the Raspberry Pi
says as much.

The four ways the container-flags problem bites:

- Your workspace fills with **root-owned files** you can't edit, because the ROS
  image runs as root and `--user` was omitted.
- A device is **listed in the container and still refused when you open it**
  (`EPERM`), because the Pi's `gpio`/`i2c`/`spi` groups are allocated dynamically
  and don't exist inside the image, so `--group-add gpio` silently grants
  nothing.
- `i2cdetect` finds **nothing on a bus that exists**, because `dtparam=i2c_arm`
  was never enabled — a firmware setting no container can fix.
- Your code targets `gpiochip4` per a Pi 5 tutorial and breaks, because
  **kernel 6.6.47 moved it back to `gpiochip0`**.

</details>

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

[Apache License 2.0](LICENSE) — the ROS ecosystem's standard, and it carries an
explicit patent grant, which matters for a project that may end up in robots.
