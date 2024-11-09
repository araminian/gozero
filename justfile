set export
set positional-arguments

start-redis:
  docker run --name gozero-redis -d -p 6379:6379 redis

stop-redis:
  docker stop gozero-redis

run dev:
  cd cmd && IS_DEV=$dev go run .

test:
  go test ./... -v