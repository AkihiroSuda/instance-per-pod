# Instance-per-Pod Admission Webhook

Instance-per-Pod Admission Webhook (IPP) creates an IaaS instance per Kubernetes `Pod` to mitigate potential container breakout attacks.
Unlike Kata Containers, IPP can even mitigate CPU vulnerabilities when baremetal instances are used.

## Supported clusters

- [X] GKE (POC)
- [ ] AKS
- [ ] EKS
- [ ] [Cluster API](https://github.com/kubernetes-sigs/cluster-api)

## Getting started

### With GKE

#### Step 1
Create a GKE node pool with the following configuration:
* Create "GCE instance metadata" (not "Kubernetes labels") `ipp-reserved=true`
* Do NOT enable autoscaling

#### Step 2
Create a GCP service account with `Comute Admin` and `Kubernetes Engine Admin` roles,
and download the JSON private key.

#### Step 3

Install IPP Admission Webhook:

```bash
IMAGE="gcr.io/$PROJECT/ipp:t$(date +%s)"
GKEPARENT="projects/$PROJECT/locations/asia-northeast1-a/clusters/$CLUSTER"
GCPSA=/path/to/gcp-sa.json

docker build -t $IMAGE . && docker push $IMAGE
./ipp.yaml.sh $IMAGE $GKEPARENT $GCPSA | kubectl apply -f -
```

You can review the YAML before running `kubectl apply`.
Note that the YAML contains `Secret` resources.

#### Step 4

Create some pods.

A pod mutated by IPP has `.spec.nodeSelector[ipp.akihirosuda.github.io/node=<generated-node-label>]` and `.metadata.labels[ipp.akihirosuda.github.io/mutated]=true`.

## Watch log

```console
$ kubectl logs -f --namespace=ipp-system deployments/ipp
```

## Uninstall

```console
$ kubectl delete mutatingwebhookconfiguration ipp
$ kubectl delete namespace ipp-system
$ kubectl delete clusterrole ipp
$ kubectl delete clusterrolebinding ipp
```

## Ignored pods

* Pods created with `DaemonSet`
* Pods in `*-system` namespaces (eg. `kube-system`)
* Pods with `ipp.akihirosuda.github.io/ignore=true` annotation

## TODO
- [ ] Allow defaulting not to use IPP
- [ ] Ignore pods with nodeSelector/nodeName/nodeAffinity...
- [ ] Reuse idle instances to save IaaS expense
- [ ] Automatically delete idle instances
- [ ] Allow annotated pods to co-exist in the same instance
- [ ] Consider more fancy project name (RFC)
