#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o errtrace

if [ "$#" -ne 3 ]; then
	echo "Usage: $0 IMAGE GKE_PARENT GCP_SERVICEACCOUNT_JSON"
	exit 1
fi
if ! command -v mkcert >/dev/null; then
	echo "Missing mkcert (https://github.com/FiloSottile/mkcert)"
	exit 1
fi
IMAGE="$1"
GKE_PARENT="$2"
GCP_SERVICEACCOUNT_JSON="$(base64 -w0 $3)"
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
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${SERVICE}
  namespace: ${NAMESPACE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ${SERVICE}
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${SERVICE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ${SERVICE}
subjects:
- kind: ServiceAccount
  name: ${SERVICE}
  namespace: ${NAMESPACE}
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
      serviceAccountName: ${SERVICE}
      containers:
      - name: ipp
        image: ${IMAGE}
        args:
        - webhook
        - --tlscert=/run/secrets/tls/tls.crt
        - --tlskey=/run/secrets/tls/tls.key
        - --gke-parent=${GKE_PARENT}
        env:
        - name: GOOGLE_APPLICATION_CREDENTIALS
          value: /run/secrets/gcp-sa/json
        ports:
        - containerPort: 443
        volumeMounts:
        - name: tls
          readOnly: true
          mountPath: /run/secrets/tls
        - name: gcp-sa
          readOnly: true
          mountPath: /run/secrets/gcp-sa
      volumes:
      - name: tls
        secret:
          secretName: ${SERVICE}-tls
      - name: gcp-sa
        secret:
          secretName: ${SERVICE}-gcp-sa
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
kind: Secret
metadata:
  name: ${SERVICE}-gcp-sa
  namespace: ${NAMESPACE}
data:
  json: "${GCP_SERVICEACCOUNT_JSON}"
type: Opaque
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
