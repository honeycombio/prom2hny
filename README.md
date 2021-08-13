# prom2hny

[![OSS Lifecycle](https://img.shields.io/osslifecycle/honeycombio/prom2hny?color=pink)](https://github.com/honeycombio/home/blob/main/honeycomb-oss-lifecycle-and-practices.md)
[![Build Status](https://travis-ci.org/honeycombio/prom2hny.svg?branch=master)](https://travis-ci.org/honeycombio/prom2hny)

**STATUS: this project is being sunset.** See https://github.com/honeycombio/prom2hny/issues/5

Scrapes Prometheus clients and sends their metrics to Honeycomb. The current
primary use case is to send kube-state-metrics data to Honeycomb.

### Usage

1. Run [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics)
    ```
    git clone https://github.com/kubernetes/kube-state-metrics
    kubectl apply -f kube-state-metrics/kubernetes
    ```

2. Deploy this utility:
    ```
    kubectl create secret generic honeycomb-writekey --from-literal=key=$YOUR_HONEYCOMB_WRITEKEY --namespace=kube-system
    kubectl apply -f kubernetes/deployment.yaml
    ```
