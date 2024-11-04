curl -k --proto-default https -H "Host: www.google.com" localhost:8443
curl -k --proto-default https -H "Host: www.trivago.com" localhost:8443
curl -k --proto-default https -H "Host: www.booking.com" localhost:8443

openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt -days 365 -nodes