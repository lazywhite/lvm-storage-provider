package provisioner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/lazywhite/nfs-storage-provider/pkg/controller"
)

type NFSProvisioner struct {
	Clientset kubernetes.Interface
	Server    string
	Path      string
	MountPath string
}

func (p *NFSProvisioner) Provision(ctx context.Context, options controller.ProvisionOptions) (*corev1.PersistentVolume, controller.ProvisioningState, error) {

	if options.PVC.Spec.Selector != nil {
		return nil, controller.ProvisioningFinished, errors.New("claim selector not supported")
	}
	pvcNS := options.PVC.Namespace
	pvcName := options.PVC.Name
	pvName := strings.Join([]string{pvcNS, pvcName, options.PVName}, "-")
	localPath := filepath.Join(p.MountPath, pvName)
	serverPath := filepath.Join(p.Path, pvName)
	if err := os.MkdirAll(localPath, 0777); err != nil {
		return nil, controller.ProvisioningFinished, errors.New("can't create directory to provision pv")
	}
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes:                   options.PVC.Spec.AccessModes,
			PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
			StorageClassName:              options.StorageClass.Name,
			MountOptions:                  options.StorageClass.MountOptions,
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: options.PVC.Spec.Resources.Requests[corev1.ResourceStorage],
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				NFS: &corev1.NFSVolumeSource{
					Server:   p.Server,
					Path:     serverPath,
					ReadOnly: false,
				},
			},
		},
	}
	return pv, controller.ProvisioningFinished, nil
}

func (p *NFSProvisioner) Delete(ctx context.Context, volume *corev1.PersistentVolume) error {
	path := volume.Spec.PersistentVolumeSource.NFS.Path
	basePath := filepath.Base(path)
	localPath := filepath.Join(p.MountPath, basePath)
	if _, err := os.Stat(localPath); err != nil {
		klog.Infof("nfs Path for pv: %s not exists, skipped...", path)
		return nil
	}
	return os.RemoveAll(localPath)
}
