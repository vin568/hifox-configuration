#!/bin/sh
# Workflow function block: fallback branch for topics outside this branch hall's closed set.
# Engine contract: block result = stdout; structured JSON on fd 3 (stdout
# hygiene), diagnostics on stderr — same convention as release-gate blocks.
set -eu
exec 3>&1 1>&2
printf '%s\n' '{"status":"rejected","reason":"topic outside closed set: apifox | docs-site"}' >&3
