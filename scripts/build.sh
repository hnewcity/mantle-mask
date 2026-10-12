#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
task_tools="$HOME/.local/share/mantle-mask-tools"
if [[ -d "$task_tools" ]]; then export PATH="$task_tools/go/bin:$task_tools/node/bin:$PATH"; fi
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/mantle-mask ./cmd/mantle-mask
printf 'Built bin/mantle-mask\n'
