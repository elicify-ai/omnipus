#!/usr/bin/env bash
# Decide whether a go-test log contains a test-hygiene guard failure.
#
# pkg/gateway TestMain prints the token GUARD FAILURE: and then exits 1.
# That log has a package FAIL line and no --- FAIL line, which is the shape
# the flake filter treats as a hang and may excuse. A guard is not a hang.
#
# Exit 0: the log contains the token. The caller must fail the gate.
# Exit 1: no token. A hang-shaped failure may still be excused.
# Sourced by the CI steps; running it directly does nothing.
ci_log_has_guard_failure() {
	[[ "$1" == *"GUARD FAILURE:"* ]]
}
