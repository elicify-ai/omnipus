#!/bin/sh
set -e

# The container always boots straight into the gateway. Onboarding is driven
# either from the web UI (visit /onboarding once the gateway is up) or from
# the interactive CLI wizard (`docker exec -it <ctr> omnipus onboard`).
#
# The previous first-run gate that invoked `omnipus onboard` here exited the
# container before the gateway could ever start — the CLI command was a
# print-only stub. See issue #159.
exec omnipus gateway "$@"
