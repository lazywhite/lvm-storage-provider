package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/reference"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type ProvisioningState string

const (
	ProvisioningFinished     ProvisioningState = "Finished"
	ProvisioningInBackground ProvisioningState = "Background"
	ProvisioningNoChange     ProvisioningState = "NoChange"
	ProvisioningReschedule   ProvisioningState = "Reschedule"

	ProvisionerAnnotation = "pv.kubernetes.io/provisioned-by"
)

type Provisioner interface {
	Provision(context.Context, ProvisionOptions) (*corev1.PersistentVolume, ProvisioningState, error)
	Delete(context.Context, *corev1.PersistentVolume) error
}

type ProvisionController struct {
	client          kubernetes.Interface
	provisionerName string
	provisioner     Provisioner

	claimInformer cache.SharedIndexInformer
	claimSynced   cache.InformerSynced
	claimQueue    workqueue.RateLimitingInterface

	volumeInformer  cache.SharedIndexInformer
	volumeSynced    cache.InformerSynced
	volumeQueue     workqueue.RateLimitingInterface
	eventRecorder   record.EventRecorder
	informerFactory informers.SharedInformerFactory
	//resyncPeriod
}

func (p *ProvisionController) filterVolume(obj interface{}) (interface{}, bool) {
	var pv *corev1.PersistentVolume
	var ok bool
	if pv, ok = obj.(*corev1.PersistentVolume); !ok {
		klog.Errorf("not a pv object: %#v", obj)
		return "", false
	}
	if pv.Annotations != nil {
		if v, ok := pv.Annotations[ProvisionerAnnotation]; ok {
			if v == p.provisionerName {
				return obj, true
			}
		}
	}
	return "", false
}

func (p ProvisionController) handleVolumeAdd(obj interface{}) {
	obj, ok := p.filterVolume(obj)
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		klog.Errorf("failed to get volume key: %s", err.Error())
	}
	if ok {
		p.volumeQueue.Add(key)
	}
}

func (p ProvisionController) handleVolumeDelete(obj interface{}) {
	return
}

func (p *ProvisionController) handleVolumeUpdate(old, new interface{}) {
	klog.Infof("volume update func triggered")
	old, ok1 := p.filterVolume(old)
	newObj, ok2 := p.filterVolume(new)

	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		klog.Errorf("failed to get volume key: %s", err.Error())
	}
	if ok1 && ok2 {
		p.volumeQueue.Add(key)
	}

}

func (p ProvisionController) handleClaimAdd(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		klog.Errorf("failed to get object NamespacedName: %s", err.Error())
		return
	}
	p.claimQueue.Add(key)
}

func (p ProvisionController) handleClaimUpdate(old interface{}, new interface{}) {
	/*
		var oldVol, newVol *corev1.PersistentVolumeClaim
		var ok bool
		if oldVol, ok = old.(*corev1.PersistentVolumeClaim);!ok{
			return
		}
		if newVol, ok = new.(*corev1.PersistentVolumeClaim);!ok{
			return
		}
		if newVol.Status.Phase == "Bound"{
			return
		}
	*/
	// not implemented
	return
}

type ProvisionOptions struct {
	StorageClass *storagev1.StorageClass
	PVName       string
	PVC          *corev1.PersistentVolumeClaim
	SelectedNode *corev1.Node
}

func NewProvisionController(client kubernetes.Interface, provisionerName string, provisioner Provisioner, options ...func(*ProvisionController) error) *ProvisionController {
	//init eventRecorder
	broadCaster := record.NewBroadcaster()
	broadCaster.StartLogging(klog.Infof)
	broadCaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: client.CoreV1().Events(corev1.NamespaceAll)})
	eventRecorder := broadCaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: provisionerName})

	claimQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "claims")
	volumeQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "volumes")

	informerFactory := informers.NewSharedInformerFactory(client, time.Second*30)

	claimInformer := informerFactory.Core().V1().PersistentVolumeClaims().Informer()
	volumeInformer := informerFactory.Core().V1().PersistentVolumes().Informer()

	controller := &ProvisionController{
		client:          client,
		provisioner:     provisioner,
		provisionerName: provisionerName,
		eventRecorder:   eventRecorder,
		informerFactory: informerFactory,
		claimInformer:   claimInformer,
		claimSynced:     claimInformer.HasSynced,
		claimQueue:      claimQueue,
		volumeInformer:  volumeInformer,
		volumeSynced:    volumeInformer.HasSynced,
		volumeQueue:     volumeQueue,
	}
	for _, option := range options {
		err := option(controller)
		if err != nil {
			klog.Fatalf("failed to apply options: %s", err.Error())
		}
	}
	claimHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { controller.handleClaimAdd(obj) },
		UpdateFunc: func(old, new interface{}) { controller.handleClaimUpdate(old, new) },
		DeleteFunc: nil,
	}
	controller.claimInformer.AddEventHandler(claimHandler)

	volumeHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { controller.handleVolumeAdd(obj) },
		UpdateFunc: func(old, new interface{}) { controller.handleVolumeUpdate(old, new) },
		DeleteFunc: func(obj interface{}) { controller.handleVolumeDelete(obj) },
	}
	controller.volumeInformer.AddEventHandler(volumeHandler)

	return controller
}

func (p *ProvisionController) Run(stopCh <-chan struct{}) {
	p.informerFactory.Start(stopCh)
	run := func() {
		klog.Infof("Starting provsion controller: %s", p.provisionerName)
		defer runtime.HandleCrash()
		defer p.claimQueue.ShutDown()
		defer p.volumeQueue.ShutDown()
		synced := cache.WaitForCacheSync(stopCh, p.claimSynced, p.volumeSynced)
		if !synced {
			klog.Fatal("Sync cache data failed")
			return
		}
		go wait.Until(func() { p.runClaimWorker() }, time.Second, stopCh)
		go wait.Until(func() { p.runVolumeWorker() }, time.Second, stopCh)
		klog.Infof("Started provison controller: %s", p.provisionerName)
		<-stopCh
		klog.Infof("Stopping provison controller: %s", p.provisionerName)
	}
	//leaderElection
	run()
}

func (p ProvisionController) runClaimWorker() {
	for p.processNextClaimItem() {
	}
}

func (p ProvisionController) runVolumeWorker() {
	for p.processNextVolumeItem() {
	}
}

func (p ProvisionController) processNextClaimItem() bool {
	obj, shutdown := p.claimQueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer p.claimQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			p.claimQueue.Forget(obj)
			return fmt.Errorf("expect string but got %#v", obj)
		}
		if err := p.syncClaimHandler(key); err != nil {
			return fmt.Errorf("failed to provision volume")
		}
		p.claimQueue.Forget(obj)
		return nil
	}()
	if err != nil {
		runtime.HandleError(err)
	}
	return true
}

func (p ProvisionController) syncVolume(key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.Errorf("failed to parse volume key: %s", key)
		return err
	}
	pv, err := p.client.CoreV1().PersistentVolumes().Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("failed to get volume: %s", key)
		return err
	}

	if pv.Status.Phase != corev1.VolumeReleased {
		return nil
	}
	if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimDelete {
		klog.Info("volume reclaim policy is not delete")
		return nil
	}
	err = p.provisioner.Delete(context.TODO(), pv)
	if err != nil {
		klog.Errorf("provisioner delete failed: %s", err.Error())
		return err
	}
	err = p.client.CoreV1().PersistentVolumes().Delete(context.TODO(), pv.Name, metav1.DeleteOptions{})
	if err != nil {
		klog.Errorf("failed to delete pv: %s", err.Error())
		return err
	}
	return nil
}

func (p ProvisionController) processNextVolumeItem() bool {
	obj, shutdown := p.volumeQueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer p.volumeQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			return fmt.Errorf("queue item is not string %#v", obj)
		}
		if err := p.syncVolume(key); err != nil {
			if errors.IsNotFound(err) {
				p.volumeQueue.Forget(obj)
				return nil
			} else {
				p.volumeQueue.AddRateLimited(obj)
				return fmt.Errorf("failed to handle item: %s requeue", err.Error())
			}
		}
		p.volumeQueue.Forget(obj)
		klog.Infof("sync volume: %s success", key)
		return nil
	}()
	if err != nil {
		runtime.HandleError(err)
	}
	return true
}

func (p ProvisionController) syncClaimHandler(key string) error {
	obj, exists, err := p.claimInformer.GetIndexer().GetByKey(key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no cache found for %s", key)
	}
	var pvc *corev1.PersistentVolumeClaim
	var ok bool
	if pvc, ok = obj.(*corev1.PersistentVolumeClaim); !ok {
		return fmt.Errorf("Cache format error for %s", key)
	}

	if pvc.Spec.VolumeName != "" {
		klog.Infof("pvc: %s already have a volume", key)
		return nil
	}
	ss, err := p.client.StorageV1().StorageClasses().Get(context.TODO(), *pvc.Spec.StorageClassName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("StorageClass %s not found", *pvc.Spec.StorageClassName)
		return err
	}
	if ss.Provisioner != p.provisionerName {
		klog.Info("provisioner not match, skipped...")
		return nil
	}
	//provision volume
	//1. get claim
	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.Errorf("key: %s error", err.Error())
		return err
	}
	claim, _ := p.client.CoreV1().PersistentVolumeClaims(ns).Get(context.TODO(), name, metav1.GetOptions{})
	pvName := p.getClaimPVName(claim)
	options := ProvisionOptions{
		PVC:          claim,
		PVName:       pvName,
		StorageClass: ss,
		SelectedNode: nil,
	}
	p.eventRecorder.Event(claim, corev1.EventTypeNormal, "Provisioning", "test")
	//pv, state, err :=  p.provisioner.Provision(ctx, options)
	pv, _, err := p.provisioner.Provision(context.TODO(), options)
	if err != nil {
		p.eventRecorder.Event(claim, corev1.EventTypeWarning, "failed", "failed")
		return err
	} else {
		//set volume's owner to claim
		claimRef, _ := reference.GetReference(scheme.Scheme, claim)
		pv.Spec.ClaimRef = claimRef
		// set volume metadata
		if pv.ObjectMeta.Annotations == nil {
			pv.ObjectMeta.Annotations = map[string]string{}
		}
		pv.ObjectMeta.Annotations[ProvisionerAnnotation] = p.provisionerName
		//add finalizer
		// create pv
		_, err := p.client.CoreV1().PersistentVolumes().Create(context.TODO(), pv, metav1.CreateOptions{})
		if err != nil {
			p.eventRecorder.Event(claim, corev1.EventTypeWarning, "Failed", "test")
			return err
		}
		p.eventRecorder.Event(claim, corev1.EventTypeNormal, "Finished", "test")
		//p.volumeQueue.Add(pv.Name)
		return nil
	}
}

func (p *ProvisionController) getClaimPVName(claim *corev1.PersistentVolumeClaim) string {
	return "pvc-" + string(claim.UID)
}
