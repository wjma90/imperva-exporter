# imperva-exporter Helm chart

This chart installs the Imperva Exporter Deployment and ClusterIP Service.

The chart does not create the Kubernetes Secret that contains Imperva
credentials. Create it before installing the chart.

## Required Secret

```bash
kubectl create namespace observability

kubectl -n observability create secret generic imperva-exporter-secret \
  --from-literal=api-id='CHANGE_ME' \
  --from-literal=api-key='CHANGE_ME' \
  --from-literal=api-base-url='https://my.incapsula.com/api/'
```

The Secret name and key names must match `existingSecret` in `values.yaml`, or
be overridden during installation.

## Install from the repository Helm repo

When the chart repository is published, add it with:

```bash
helm repo add imperva-exporter https://wjma90.github.io/imperva-exporter
helm repo update
```

Install or upgrade:

```bash
helm upgrade --install imperva-exporter imperva-exporter/imperva-exporter \
  --namespace observability \
  --create-namespace \
  --set existingSecret.name=imperva-exporter-secret \
  --set image.tag=main
```

## Install from a local checkout

```bash
helm upgrade --install imperva-exporter ./charts/imperva-exporter \
  --namespace observability \
  --create-namespace \
  --set existingSecret.name=imperva-exporter-secret \
  --set image.tag=main
```
