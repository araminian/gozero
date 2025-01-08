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

We need to have two Kubernetes resources to use GoZero:

1. `VirtualService` to route the request to GoZero, then GoZero will route the request to the target service.
```yaml
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
  name: app
  namespace: app-a
spec:
  hosts:
  - app-app-a.k8s.orb.local # The host of the request.
  gateways:
  - istio-system/ingress # The gateway to the request.
  http:
  - headers:
      request:
        add:
          X-Gozero-Target-Port: "3000" # The port of the target service.
          X-Gozero-Target-Host: "app.app-a.svc.cluster.local" # The host of the target service.
          X-Gozero-Target-Retries: "10" # The number of retries to the target service. (optional)
          X-Gozero-Target-Backoff: "100ms" # The backoff time to the target service. (optional)
    route:
    - destination:
        host: gozero.gozero.svc.cluster.local # The GoZero service.
        port:
          number: 8443 # The proxy port of the GoZero service.
    retries:
      attempts: 1
      perTryTimeout: 300s # Set it higher than the time that the target service takes to be ready.
```

2. KEDA `ScaledObject` to scale the target service.

```bash
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: app-a
  namespace: app-a
spec:
  pollingInterval: 10 # The interval to check the metrics.
  cooldownPeriod: 30 # The cooldown period to scale down the target service.
  minReplicaCount: 0 # The minimum number of replicas of the target service.
  maxReplicaCount: 2 # The maximum number of replicas of the target service.
  scaleTargetRef:
    name: app # The name of the target service.
    apiVersion: apps/v1
    kind: Deployment
  triggers:
  - type: metrics-api
    metadata:
      targetValue: "1" # The target value to scale the target service, which will be compared with value from the metrics which is 10 by default.
      format: "json"
      activationTargetValue: "1" # When should enable scale up from zero. The target value to activate the scaling, which will be compared with value from the metrics which is 10 by default.
      url: "http://gozero.gozero.svc.cluster.local:9090/metrics/app-app-a-svc-cluster-local" # Replace the . with - in the host of the request.
      valueLocation: "value"

```

For more information about the `ScaledObject`, please refer to the [KEDA ScaledObject Spec](https://keda.sh/docs/2.16/reference/scaledobject-spec/).

## Design

You can find the design of `GoZero` in [Design](./docs/design.md) page.

## FAQ

You can find the FAQ of `GoZero` in [FAQ](./docs/faq.md) page.

