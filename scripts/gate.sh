#!/usr/bin/env bash
# Deterministic pre-merge gate. Run by CI (the required `test` check) and locally
# before pushing. Mirrors the deterministic steps of the no-mistakes pipeline
# (format, lint, test) plus the repo's docs ASCII check. The AI gates (review,
# docs-sync) run separately - see docs/operations/validation-pipeline.md.
#
# Stack is auto-detected. Absent tooling (docs-only phase) skips cleanly and
# stays green, so gates fill in as code lands. Runs every gate, reports each,
# and exits non-zero if any failed. No side effects; preserves failure exit.
set -uo pipefail

failed=0

gate() {
  # gate <name> <cmd...>; runs the command, records pass/fail, never aborts early.
  local name=$1
  shift
  if "$@"; then
    printf 'PASS  %s\n' "$name"
  else
    printf 'FAIL  %s\n' "$name" >&2
    failed=1
  fi
}

skip() { printf 'SKIP  %s (%s)\n' "$1" "$2"; }

# --- docs: printable ASCII in markdown ---------------------------------------
ascii_gate() {
  ! grep -rPn '[^\x20-\x7E\t]' --include='*.md' \
    --exclude-dir=.git --exclude-dir=node_modules .
}
gate "docs: markdown is printable ASCII" ascii_gate

# --- go: format, vet, test ---------------------------------------------------
if [ -f go.mod ] || find . -name go.mod -not -path './.git/*' -print -quit | grep -q .; then
  go_fmt_gate() { [ -z "$(gofmt -l .)" ] || { gofmt -l . >&2; false; }; }
  gate "go: gofmt" go_fmt_gate
  gate "go: vet" go vet ./...
  gate "go: test" go test ./...
else
  skip "go: format/vet/test" "no go.mod"
fi

# --- node: format-check, lint, test (only scripts that exist) ----------------
if [ -f package.json ]; then
  npm_script() { node -e "process.exit(require('./package.json').scripts?.['$1']?0:1)" 2>/dev/null; }
  for s in format:check lint test; do
    if npm_script "$s"; then
      gate "node: npm run $s" npm run "$s" --silent
    else
      skip "node: npm run $s" "no such script"
    fi
  done
else
  skip "node: format-check/lint/test" "no package.json"
fi

if [ "$failed" -ne 0 ]; then
  printf '\ngate: FAILED\n' >&2
  exit 1
fi
printf '\ngate: passed\n'
