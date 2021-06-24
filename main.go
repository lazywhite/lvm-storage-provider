package main

import (
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/lazywhite/nfs-storage-provider/pkg/controller"
	"github.com/lazywhite/nfs-storage-provider/pkg/provisioner"
	"github.com/lazywhite/nfs-storage-provider/pkg/signal"
	//"sigs.k8s.io/sig-storage-lib-external-provisioner/v6/controller"
)

var (
	server       string
	path         string
	kubeconfig   string
	providerName = "nfs-provider"
)

func init() {
	server = os.Getenv("NFS_SERVER")
	if server == "" {
		klog.Fatal("no nfs server set")
	}
	path = os.Getenv("NFS_PATH")
	if path == "" {
		klog.Fatal("no nfs path set")
	}
	kubeconfig = os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		klog.Fatal("no kubeconfig set")
	}
}

//接口检查
var _ controller.Provisioner = &provisioner.NFSProvisioner{}

func main() {
	/*
		Todo
			Qualifier
			ReclaimPolicy
			LeaderElection
			ProvisionState
				retry
			pvc update handler
		    when pvc be removed, bounded pv status will update to Released 
			multi worker
			metric
	*/
	stopCh := signal.SetupSignalHandler()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != err {
		klog.Fatal("failed to build kubeconfig")
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != err {
		klog.Fatal("failed to build clientset")
	}

	nfsProvisioner := &provisioner.NFSProvisioner{
		Clientset: clientset,
		Server:    server,
		Path:      path,
		MountPath: "/data",
	}

	//pc := controller.NewProvisionController(clientset, providerName, provisioner, "1.14.8")
	pc := controller.NewProvisionController(clientset, providerName, nfsProvisioner)
	pc.Run(stopCh)
}
