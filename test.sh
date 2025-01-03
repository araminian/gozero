curl -k --proto-default https -H "Host: www.google.com" localhost:8443
curl -k --proto-default https -H "Host: www.trivago.com" localhost:8443
curl -k --proto-default https -H "Host: www.booking.com" localhost:8443

openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt -days 365 -nodes

curl -X GET   -H "X-Gozero-Target-Port: 3000"   -H "X-Gozero-Target-Host: app.app-a.svc.cluster.local"   -H "X-Gozero-Target-Scheme: http"   -H "X-Gozero-Target-Retries: 10"   -H "X-Gozero-Target-Backoff: 100ms" gozero.gozero.svc.cluster.local:8443/pass -vvv

go run . grpc-server-grpc-server.k8s.orb.local:80
curl http://app-app-a.k8s.orb.local/pass -v