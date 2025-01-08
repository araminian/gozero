# FAQ

## What is GoZero

`GoZero` is a reverse-proxy that can be deployed on Kubernetes. It can route HTTP/1.1, HTTP/2 (GRPC) requests to different services. And it can provide a way to scale services from zero to desired number of replicas and vice versa by relying on `KEDA`.

## Why I create this project

We have a lot of services running on Kubernetes, we would like to provide preview environments for our users. But we don't want to spend a lot of money for unused resources. So we need a way to scale preview environments from zero to desired number of replicas and vice versa.

We also tried to use KEDA HTTP-ADDON, but it's not a good fit for our use cases.

### Why not KEDA HTTP-ADDON

KEDA HTTP-Addon is a good project, which supports scaling `HTTP/1.1` services based on HTTP requests. It can scale the number of replicas based on the number of HTTP requests. We think it's overkill for our use cases and not support `HTTP/2 (GRPC)` services.

