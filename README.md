# GOZERO

`GoZero` is a reverse-proxy that can be deployed on Kubernetes. It can route HTTP/1.1, HTTP/2 (GRPC) requests to different services. And it can provide a way to scale services from zero to desired number of replicas and vice versa by relying on `KEDA`.

## How to install GoZero

There are two ways to install GoZero:

1. Using the `manifests` directory, which contains the Kubernetes manifests for GoZero.
```bash
kubectl apply -f manifests/manifests.yaml
```

2. Using the `chart` directory, which contains the Helm chart for GoZero.
```bash
helm install gozero ./chart/gozero
```

## How to use GoZero

TODO

## Design

You can find the design of `GoZero` in [Design](./docs/design.md) page.

## FAQ

You can find the FAQ of `GoZero` in [FAQ](./docs/faq.md) page.

