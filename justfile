set export
set positional-arguments

build VERSION:
  #!/usr/bin/env bash
  set -euo pipefail
  export GIT_COMMIT=$(git rev-parse --short=7 HEAD)
  skaffold build

start-redis:
  docker run --name gozero-redis -d -p 6379:6379 redis

stop-redis:
  docker stop gozero-redis

run dev:
  cd cmd && IS_DEV=$dev go run .

test:
  go test ./... -v