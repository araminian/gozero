start-redis:
  docker run --name gozero-redis -d -p 6379:6379 redis

stop-redis:
  docker stop gozero-redis

run:
  cd cmd && go run .

test:
  go test ./... -v