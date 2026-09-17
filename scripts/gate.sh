#!/usr/bin/env bash
# deterministic gate for ci `test` check + pre-commit hook; absent tooling skips green
set -uo pipefail

failed=0

gate() {
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

ascii_gate() {
  ! grep -rPn '[^\x20-\x7E\t]' --include='*.md' \
    --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=.worktrees \
    --exclude-dir=.next .
}
gate "docs: markdown is printable ASCII" ascii_gate

go_fmt_gate() {
  local out
  out=$(cd "$1" && gofmt -l .)
  [ -z "$out" ] || { printf '%s\n' "$out" >&2; false; }
}
go_in() { local dir=$1; shift; (cd "$dir" && "$@"); }

if command -v go >/dev/null 2>&1; then
  found_go=0
  while IFS= read -r mod; do
    found_go=1
    dir=$(dirname "$mod")
    gate "go: gofmt ($dir)" go_fmt_gate "$dir"
    gate "go: vet ($dir)" go_in "$dir" go vet ./...
    gate "go: test ($dir)" go_in "$dir" go test ./...
    gate "go: build ($dir)" go_in "$dir" go build ./...
  done < <(find . -name go.mod \
    -not -path './.git/*' -not -path '*/node_modules/*' -not -path './.worktrees/*')
  [ "$found_go" = 1 ] || skip "go: format/vet/test/build" "no go.mod"
else
  skip "go: format/vet/test/build" "go not installed"
fi

tf_validate_gate() {
  (cd "$1" && terraform init -backend=false -input=false -no-color >/dev/null &&
    terraform validate -no-color)
}
if command -v terraform >/dev/null 2>&1; then
  if [ -d deploy/terraform ]; then
    gate "terraform: fmt" terraform -chdir=deploy/terraform fmt -recursive -check
    for envdir in deploy/terraform/envs/*/; do
      [ -f "$envdir/main.tf" ] || continue
      gate "terraform: validate ($envdir)" tf_validate_gate "$envdir"
    done
  else
    skip "terraform: fmt/validate" "no deploy/terraform"
  fi
else
  skip "terraform: fmt/validate" "terraform not installed"
fi

npm_script() {
  (cd "$1" && node -e "process.exit(require('./package.json').scripts?.['$2']?0:1)" 2>/dev/null)
}
npm_in() { local dir=$1; shift; (cd "$dir" && "$@"); }

node_modules_stale() {
  [ ! -d "$1/node_modules" ] ||
    [ "$1/package-lock.json" -nt "$1/node_modules/.package-lock.json" ]
}

found_node=0
while IFS= read -r pkg; do
  found_node=1
  dir=$(dirname "$pkg")
  if node_modules_stale "$dir"; then
    gate "node: npm ci ($dir)" npm_in "$dir" npm ci --silent
  fi
  for s in format:check lint test build; do
    if npm_script "$dir" "$s"; then
      gate "node: npm run $s ($dir)" npm_in "$dir" npm run "$s" --silent
    else
      skip "node: npm run $s ($dir)" "no such script"
    fi
  done
done < <(find . -maxdepth 3 -name package.json \
  -not -path '*/node_modules/*' -not -path './.git/*' -not -path './.worktrees/*')
[ "$found_node" = 1 ] || skip "node: format-check/lint/test/build" "no package.json"

if [ "$failed" -ne 0 ]; then
  printf '\ngate: FAILED\n' >&2
  exit 1
fi
printf '\ngate: passed\n'
