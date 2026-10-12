#!/usr/bin/env bash
# Local-only container checks. No AWS credentials or inference requests.
set -euo pipefail
image="${1:-mantle-mask:smoke}"
name="mantle-mask-smoke-$$"
volume="$name-data"
token="docker-smoke-token-not-for-deployment"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
[[ "$(docker image inspect "$image" --format '{{.Config.User}}')" == "65532:65532" ]]
docker volume create "$volume" >/dev/null
docker run -d --name "$name" -p 127.0.0.1::8080 --mount "type=volume,src=$volume,dst=/app/data" \
  -e "MANTLE_ADMIN_TOKEN=$token" "$image" >/dev/null
port=$(docker port "$name" 8080/tcp | head -n1 | cut -d: -f2)
url="http://127.0.0.1:$port"
ready() {
  for attempt in $(seq 1 30); do
    if curl -fsS "$url/healthz" >/dev/null; then return; fi
    sleep 1
  done
  docker logs "$name"
  return 1
}
ready
curl -fsS "$url/" | grep -q 'id="root"'
[[ "$(curl -s -o /dev/null -w '%{http_code}' "$url/api/admin/overview")" == 401 ]]
curl -fsS -H "Authorization: Bearer $token" "$url/api/admin/overview" >/dev/null
curl -fsS -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d '{"name":"docker-persistence-check","note":"fixture","enabled":true}' "$url/api/admin/users" >/dev/null
docker restart "$name" >/dev/null
ready
curl -fsS -H "Authorization: Bearer $token" "$url/api/admin/users" | grep -q 'docker-persistence-check'
echo 'Docker smoke passed: non-root, embedded UI, auth, writable volume, restart persistence.'
