FROM golang:1.23.2-alpine AS base
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o gozero main.go

FROM alpine:latest
WORKDIR /app
USER 1001
COPY --from=base /app/gozero .
EXPOSE 8443
CMD ["./gozero"]
