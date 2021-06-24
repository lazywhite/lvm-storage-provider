## Introduction
A toy nfs volume provisioner, referenced [https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner]

## Usage
```
1. make sure you have a running NFS service
2. modify run.sh; then run it
3. create or delete pvc to view the result
    kubectl apply -f deploy/test-claim.yaml
    kubectl delete -f deploy/test-claim.yaml
```
