# Instance-per-Pod Admission Webhook

Instance-per-Pod Admission Webhook (IPP) creates an IaaS instance per Kubernetes `Pod` to mitigate potential container breakout attacks.

Unlike Kata Containers, IPP can even mitigate CPU vulnerabilities when baremetal instances (e.g. [EC2 `i3.metal`](https://aws.amazon.com/jp/ec2/instance-types/i3/)) are used.

## Requirements

* [Cluster Autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler) must be enabled.

* [NodeRestriction Admission Controller](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#noderestriction) or its equivalent must be enabled.
  Without NodeRestriction or its equivalent, IPP is not useful because a compromised node can run privileged pods on other nodes using the kubelet's credential.
  NodeRestriction is enabled by default on typical clusters including Google Kubernetes Engine (GKE) and Amazon Elastic Kubernetes Service (EKS).
  However, [it is not enabled on Azure Kubernetes Service (AKS)](https://github.com/Azure/aks-engine/issues/2422), as of December 2019.

Tested on Google Kubernetes Engine (GKE).

## How it works

IPP Admission Webhook is implemented using [Cluster Autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler), [Tolerations](https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/), [Node Affinity, and Pod Anti-Affinity](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/).

See [#2](https://github.com/AkihiroSuda/instance-per-pod/issues/2) for the design.

## Getting started

### Step 1

Create a GKE node pool with the following configuration:
* Enable autoscaling. The minimum number of the nodes can be zero.
* Add node label: `"ipp" = "true"`
* Add node taint: `"ipp" = "true"`  (`NO_SCHEDULE` mode)

If you choose to use other label and taint names, you need to modify the YAML in Step 2 accordingly.

Non-GKE clusters should work as well, but not tested.

### Step 2

Install IPP Admission Webhook:

```bash
docker build -t $IMAGE . && docker push $IMAGE
./ipp.yaml.sh $IMAGE | kubectl apply -f -
```

You can review the YAML before running `kubectl apply`.
Note that the YAML contains `Secret` resources.

### Step 3

Create Pods with various `ipp-class` labels.

e.g.
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: foo
  labels:
    app: foo
    ipp-class: class0
spec:
  selector:
    matchLabels:
      app: foo
  template:
    metadata:
      labels:
        app: foo
        ipp-class: class0
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
```

IPP Admission Webhook automatically translates the Pod manifests as follows:

```yaml
apiVersion: v1
kind: Pod
...
spec:
  tolerations:
  - effect: NoSchedule
    key: ipp
    operator: Equal
    value: "true"
...
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: ipp
            operator: In
            values:
            - "true"
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchExpressions:
          - key: ipp-class
            operator: NotIn
            values:
            - class0
        topologyKey: kubernetes.io/hostname
...
```

Pods with different `ipp-class` label values are never colocated on the same node.

When the existing node set is not sufficient to satisfy the scheduling constraint, the Cluster Autoscaler automatically adds a node.
On GKE, creating a node takes about a minute.

The cluster autoscaler also automatically remove idle nodes.
On GKE, an idle node is removed when it has been idle for about 10 minutes.

## Troubleshooting

If it doesn't work as expected, check the log from the IPP Admission Webhook:

```console
kubectl logs -f --namespace=ipp-system deployments/ipp
```

## Uninstall

```console
kubectl delete mutatingwebhookconfiguration ipp
kubectl delete namespace ipp-system
```

## Caveats

### Best-effort

IPP Admission Webhook does not provide any guarantee for the actual Pod scheduling.

### Scheduling overhead

The current implementation of IPP Admission Webhook is implemented using Pod Anti-Affinity, which doesn't really scale.

> Unfortunately, the current implementation of the affinity predicate in scheduler is about 3 orders of magnitude slower than for all other predicates combined, and it makes CA hardly usable on big clusters.
> https://github.com/kubernetes/autoscaler/blob/6ab78a85e19d55bd9c0ff1cb9f9f588a46522d6e/cluster-autoscaler/FAQ.md#what-are-the-service-level-objectives-for-cluster-autoscaler

For large clusters, we should also support affinity-less mode, which would explicitly call the IaaS API for creating and removing dedicated IaaS instances.
Acutally, an early release of IPP Admission Webhook ([v0.0.1](https://github.com/AkihiroSuda/instance-per-pod/tree/v0.0.1)) was implemented like that.


### DaemonSet

IPP Admission Webhook does not mutate DaemonSet, so that system daemon Pods can be colocated with IPP Pods.
