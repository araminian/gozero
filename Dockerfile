FROM golang:1.23.3-alpine AS base
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o gozero cmd/main.go

FROM alpine:3.21.1
ARG VERSION
ARG GIT_COMMIT
ENV VERSION=$VERSION
ENV GIT_COMMIT=$GIT_COMMIT
WORKDIR /app
USER 1001
COPY --from=base /app/gozero .
EXPOSE 8443
CMD ["./gozero"]