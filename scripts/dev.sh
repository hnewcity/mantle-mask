#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
task_tools="$HOME/.local/share/mantle-mask-tools"
if [[ -d "$task_tools" ]]; then export PATH="$task_tools/go/bin:$task_tools/node/bin:$PATH"; fi
if [[ ! -d web/node_modules ]]; then npm --prefix web ci; fi
go build -o bin/mantle-mask ./cmd/mantle-mask
./bin/mantle-mask &
task_backend_pid=$!
trap 'kill "$task_backend_pid" 2>/dev/null || true' EXIT INT TERM
npm --prefix web run dev
