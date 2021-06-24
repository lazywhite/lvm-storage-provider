export NFS_SERVER=192.168.56.102
export NFS_PATH=/nfsdata
export PROVISIONER_NAME=nfs-provider

export KUBECONFIG=/root/.kube/config

go run main.go
