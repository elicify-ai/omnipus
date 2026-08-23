#!/bin/sh
# Smoke-test the runtime tooling baked into the Omnipus container image.
#
# ADR-067 collapsed the image matrix to ONE image (docker/Dockerfile.heavy),
# so this no longer goes through a compose file. docker-compose.full.yml was
# deleted with the minimal/full split; docker-compose.yml that remains is
# pull-only (it references a published ghcr.io tag and has no build section),
# so `docker compose build` against it is a silent no-op rather than a build.
# We build the image directly and exercise it with `docker run`.

set -eu

IMAGE="${OMNIPUS_TEST_IMAGE:-omnipus:mcp-smoke}"
DOCKERFILE="docker/Dockerfile.heavy"

echo "Testing runtime tooling in the Omnipus container image..."
echo ""

echo "Building $IMAGE from $DOCKERFILE..."
docker build -f "$DOCKERFILE" -t "$IMAGE" .
echo ""

failures=0

# check <label> <shell-command>
check() {
	label="$1"
	shift
	printf '  %-28s' "$label"
	if out=$(docker run --rm --entrypoint sh "$IMAGE" -c "$*" 2>&1); then
		printf 'OK   %s\n' "$(printf '%s' "$out" | head -1)"
	else
		rc=$?
		printf 'FAIL (exit %d)\n' "$rc"
		printf '%s\n' "$out" | sed 's/^/      /'
		failures=$((failures + 1))
	fi
}

check "node"    'node --version'
check "npm"     'npm --version'
check "npx"     'npx --version'
check "git"     'git --version'
check "python3" 'python3 --version'
check "uv"      'uv --version'

# The MCP server runs until killed, so `timeout` reporting 124 is the SUCCESS
# signal: it means the package installed and the server started. This used to
# end in `|| true`, which made the check incapable of failing — a broken npx
# or an unreachable registry still reported a passing test.
printf '  %-28s' "mcp server starts"
mcp_out=$(docker run --rm --entrypoint sh "$IMAGE" -c \
	'</dev/null timeout 5 npx -y @modelcontextprotocol/server-filesystem /tmp' 2>&1) && mcp_rc=0 || mcp_rc=$?
if [ "$mcp_rc" -eq 124 ] || [ "$mcp_rc" -eq 0 ]; then
	printf 'OK   (exit %d — started, then timed out as expected)\n' "$mcp_rc"
else
	printf 'FAIL (exit %d — expected 0 or 124)\n' "$mcp_rc"
	printf '%s\n' "$mcp_out" | sed 's/^/      /'
	failures=$((failures + 1))
fi

echo ""
if [ "$failures" -ne 0 ]; then
	echo "$failures check(s) FAILED."
	exit 1
fi
echo "All checks passed."
