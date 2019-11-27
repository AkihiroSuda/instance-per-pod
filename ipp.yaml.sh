#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o errtrace

if [ "$#" -ne 1 ]; then
	echo "Usage: $0 IMAGE"
	exit 1
fi
if ! command -v mkcert >/dev/null; then
	echo "Missing mkcert (https://github.com/FiloSottile/mkcert)"
	exit 1
fi
IMAGE="$1"
NAMESPACE="ipp-system"
SERVICE="ipp"
SAN="${SERVICE}.${NAMESPACE}.svc"

tmp=$(mktemp -d ipp-secret.XXXXXXXXXX)
(
	cd $tmp
	CAROOT=. mkcert $SAN >/dev/null 2>&1
)

TLSCERT="$(base64 -w0 $tmp/${SAN}.pem)"
TLSKEY="$(base64 -w0 $tmp/${SAN}-key.pem)"
TLSCA="$(base64 -w0 $tmp/rootCA.pem)"
rm -rf $tmp

cat <<EOF
# WARNING: this yaml contains secret values!
kind: Namespace
apiVersion: v1
metadata:
  name: ${NAMESPACE}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${SERVICE}
  namespace: ${NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${SERVICE}
  template:
    metadata:
      labels:
        app: ${SERVICE}
    spec:
      containers:
      - name: ipp
        image: ${IMAGE}
        args:
        - webhook
        - --tlscert=/run/secrets/tls/tls.crt
        - --tlskey=/run/secrets/tls/tls.key
# The node label used for the IPP autoscaling node pool.
# The label value should be "true".
# NOTE: GKE doesn't seem to support "node-restriction.kubernetes.io/" prefix
        - --node-label=ipp
# The node taint used for the same node pool
# The taint value should be "true".
        - --node-taint=ipp
# The pod label, e.g. "ipp-class=class0"
        - --pod-label=ipp-class
        ports:
        - containerPort: 443
        volumeMounts:
        - name: tls
          readOnly: true
          mountPath: /run/secrets/tls
      volumes:
      - name: tls
        secret:
          secretName: ${SERVICE}-tls
---
apiVersion: v1
kind: Secret
metadata:
  name: ${SERVICE}-tls
  namespace: ${NAMESPACE}
data:
  tls.crt: "${TLSCERT}"
  tls.key: "${TLSKEY}"
type: kubernetes.io/tls
---
apiVersion: v1
kind: Service
metadata:
  name: ${SERVICE}
  namespace: ${NAMESPACE}
spec:
  ports:
  - port: 443
    targetPort: 443
  selector:
    app: ${SERVICE}
---
apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingWebhookConfiguration
metadata:
  name: ${SERVICE}
webhooks:
- name: ${SAN}
  failurePolicy: Ignore
  rules:
  - operations: ["CREATE"]
    apiGroups: [""]
    apiVersions: ["v1"]
    resources: ["pods"]
  clientConfig:
    service:
      name: ${SERVICE}
      namespace: ${NAMESPACE}
      path: /admission
    caBundle: "${TLSCA}"
EOF
