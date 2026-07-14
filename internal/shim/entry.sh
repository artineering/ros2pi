#!/usr/bin/env bash
# ros2pi container entry shim.
#
# Mounted read-only into the container and invoked as:
#   bash /usr/local/lib/ros2pi/entry.sh <command> [args...]
#
# It sources the ROS environment, verifies it is the one the workspace asked
# for, and then execs the command.
#
# -e is the reason this file exists: a failed source must NOT be shrugged off.
# -u is deliberately NOT set globally: ROS's own setup.bash reads unbound
# variables (AMENT_TRACE_SETUP_FILES), so -u would make sourcing a correct ROS
# installation fail. It is enabled only around our own logic, where it protects
# against typos in this script.
set -eo pipefail
set -u

fail() {
    echo "ros2pi: $*" >&2
    exit 78 # EX_CONFIG: a configuration problem, not a failure of the command
}

# ROS2PI_EXPECT_DISTRO is set from ros2pi.toml's ros.distro.
expect="${ROS2PI_EXPECT_DISTRO:-}"
[ -n "$expect" ] || fail "ROS2PI_EXPECT_DISTRO is not set; this container was not created by ros2pi"

setup="/opt/ros/${expect}/setup.bash"

# This check is the whole reason the shim exists.
#
# Sourcing a missing setup.bash and carrying on is NOT harmless: the ROS image's
# own entrypoint has already sourced ITS distro by the time we run, so a
# workspace configured for humble against a jazzy image would find ros2 on PATH,
# report 194 packages, and work -- as jazzy. The user would never be told.
if [ ! -f "$setup" ]; then
    have="${ROS_DISTRO:-none}"
    fail "this image does not contain ROS 2 ${expect}
  ros2pi.toml says   ros.distro = ${expect}
  the image provides ROS_DISTRO = ${have}
  ${setup} does not exist

  Fix one of:
    - set ros.distro = \"${have}\" in ros2pi.toml
    - set ros.image to an image containing ${expect} (e.g. ros:${expect})"
fi

# ROS's setup scripts are not -u clean, so relax it across the source only.
# -e stays on: a source that fails must still stop us.
set +u
# shellcheck disable=SC1090
source "$setup"
set -u

# Verify rather than trust: if the image were unusual enough that sourcing left
# a different distro active, silently proceeding would reintroduce exactly the
# bug guarded against above.
if [ "${ROS_DISTRO:-}" != "$expect" ]; then
    fail "sourced ${setup} but ROS_DISTRO is ${ROS_DISTRO:-empty}, expected ${expect}"
fi

# The workspace overlay, when it has been built. Absent before the first build,
# which is normal and not an error.
if [ -f "/ros2_ws/install/setup.bash" ]; then
    set +u
    # shellcheck disable=SC1091
    source /ros2_ws/install/setup.bash
    set -u
fi

[ "$#" -gt 0 ] || set -- bash

exec "$@"
