#!/usr/bin/env bash
# Time lnpm against yalc on the same package, both as subprocesses.
#
# tests/bench_test.go also compares the two, but it calls lnpm's Run* functions
# in-process while spawning yalc as a command. That charges yalc for a Node
# startup lnpm never pays and inflates the ratio. Everything here goes through
# an installed binary, so the startup cost is on both sides.
set -euo pipefail

ITERATIONS="${ITERATIONS:-10}"
FILES="${FILES:-100}"

command -v yalc >/dev/null || { echo "yalc not on PATH: npm install -g yalc" >&2; exit 1; }
command -v go >/dev/null || { echo "go not on PATH" >&2; exit 1; }

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

lnpm_bin="$work/lnpm"
go build -o "$lnpm_bin" "$repo_root/cmd/lnpm"

export LNPM_STORE="$work/store"
export YALC_STORE_DIR="$work/yalc-store"

# A fresh copy per iteration: publish is content-addressed on both sides, and
# timing a re-publish of bytes already in the store measures the cache.
seed_package() {
  local dir=$1 name=$2 salt=$3
  mkdir -p "$dir"
  printf '{"name":"%s","version":"1.0.0"}\n' "$name" > "$dir/package.json"
  local i
  for ((i = 0; i < FILES; i++)); do
    printf 'module.exports = { id: %d, salt: "%s" };\n' "$i" "$salt" > "$dir/file-$i.js"
  done
}

seed_project() {
  local dir=$1
  mkdir -p "$dir"
  printf '{"name":"consumer","version":"1.0.0"}\n' > "$dir/package.json"
}

# Elapsed nanoseconds for one command run in one directory, stdout and stderr
# dropped so terminal I/O is not part of the measurement.
time_once() {
  local dir=$1; shift
  local start end
  start=$(date +%s%N)
  (cd "$dir" && "$@" >/dev/null 2>&1)
  end=$(date +%s%N)
  echo $((end - start))
}

# Mean milliseconds over ITERATIONS, to one decimal place.
mean_ms() {
  local total=0 sample
  for sample in "$@"; do total=$((total + sample)); done
  awk -v t="$total" -v n="$#" 'BEGIN { printf "%.1f", t / n / 1000000 }'
}

bench_publish() {
  local tool=$1 samples=() i pkg
  for ((i = 0; i < ITERATIONS; i++)); do
    pkg="$work/$tool-pub-$i"
    seed_package "$pkg" "$tool-pub" "iter-$i"
    case $tool in
      lnpm) samples+=("$(time_once "$pkg" "$lnpm_bin" publish)") ;;
      yalc) samples+=("$(time_once "$pkg" yalc publish --no-scripts)") ;;
    esac
  done
  mean_ms "${samples[@]}"
}

bench_add() {
  local tool=$1 samples=() i pkg proj
  pkg="$work/$tool-add-src"
  seed_package "$pkg" "$tool-add" fixed
  case $tool in
    lnpm) (cd "$pkg" && "$lnpm_bin" publish >/dev/null 2>&1) ;;
    yalc) (cd "$pkg" && yalc publish --no-scripts >/dev/null 2>&1) ;;
  esac
  for ((i = 0; i < ITERATIONS; i++)); do
    proj="$work/$tool-add-proj-$i"
    seed_project "$proj"
    case $tool in
      lnpm) samples+=("$(time_once "$proj" "$lnpm_bin" add "$tool-add")") ;;
      yalc) samples+=("$(time_once "$proj" yalc add "$tool-add")") ;;
    esac
  done
  mean_ms "${samples[@]}"
}

printf 'Package: %s files. Iterations: %s. Both tools run as subprocesses.\n\n' \
  "$((FILES + 1))" "$ITERATIONS"
printf '%-10s %12s %12s %10s\n' operation lnpm yalc speedup
printf '%-10s %12s %12s %10s\n' --------- ---------- ---------- -------

for op in publish add; do
  l=$("bench_$op" lnpm)
  y=$("bench_$op" yalc)
  printf '%-10s %11sms %11sms %9sx\n' "$op" "$l" "$y" \
    "$(awk -v l="$l" -v y="$y" 'BEGIN { printf "%.1f", y / l }')"
done
