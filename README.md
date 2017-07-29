[Under development]

Scrapes Prometheus clients and sends their metrics to Honeycomb. The current
primary use case is to send kube-state-metrics data to Honeycomb.

### Usage

1. Run [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics)

2. Deploy this utility:
    ```
    kubectl create secret generic honeycomb-writekey --from-literal=key=$YOUR_HONEYCOMB_WRITEKEY --namespace=kube-system
    kubectl apply -f kubernetes/deployment.yaml
    ```
