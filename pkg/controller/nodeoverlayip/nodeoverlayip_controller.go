package nodeoverlayip

import (
	"context"
	"strings"
	"reflect"

	ipam "github.com/jkwong888/iks-overlay-ip-controller/pkg/ipam"
	iksv1alpha1 "github.com/jkwong888/iks-overlay-ip-controller/pkg/apis/iks/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_nodeoverlayip")

// Add creates a new NodeOverlayIP Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileNodeOverlayIP{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("nodeoverlayip-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource NodeOverlayIP
	err = c.Watch(&source.Kind{Type: &iksv1alpha1.NodeOverlayIp{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileNodeOverlayIP implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileNodeOverlayIP{}

// ReconcileNodeOverlayIP reconciles a Node object
type ReconcileNodeOverlayIP struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a NodeOverlayIP object and makes changes based on the state read
// and what is in the NodeOverlayIP.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileNodeOverlayIP) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Name", request.Name)
	reqLogger.Info("Reconciling NodeOverlayIp")

	// Fetch the NodeOverlayIP instance
	instance := &iksv1alpha1.NodeOverlayIp{}

	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("NodeOverlayIp seems to have been deleted?")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// add finalizer
	reqLogger.Info("Adding Finalizer for the NodeOverlayIp")
	if err := r.addFinalizer(instance); err != nil {
		reqLogger.Error(err, "Failed to update NodeOverlayIp with finalizer")
		return reconcile.Result{}, err
	}

	phpIPAM, err := ipam.NewPhpIPAM()
	if err != nil {
		return reconcile.Result{}, err
	}

	// someone deleted the IP, clean up IPAM
	isDeleted := instance.GetDeletionTimestamp() != nil
	if isDeleted {
		if instance.Status.IpAddr != "" {
			// remove the mask from the ip address
			ipAddrArr := strings.Split(instance.Status.IpAddr, "/")
			err = phpIPAM.DeleteIPAddress(ipAddrArr[0])
			if err != nil {
				return reconcile.Result{}, err
			}
			instance.Status.IpAddr = ""
			instance.Status.Gateway = ""
		}

		// remove the finalizers if the IP address could be removed from IPAM, so kube
		// will clean up
		instance.SetFinalizers(nil)
		err = r.client.Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	// Update the status 
	status := instance.Status
	if status.IpAddr == "" {
		zone := instance.GetLabels()["zone"]
		// reserve an IP
		myIP, err := phpIPAM.ReserveIPAddress(instance.Name, zone)
		if err != nil {
			return reconcile.Result{}, err
		}

		status.IpAddr = myIP
		reqLogger.Info("Reserved IP", "ipAddr", myIP)
	}

	if status.Gateway == "" {
		ipAddrArr := strings.Split(status.IpAddr, "/")
		reqLogger.Info("Find gateway", "ipAddr", ipAddrArr[0])
		mySubnet, err := phpIPAM.GetSubnetForIP(ipAddrArr[0])
		if err != nil {
			return reconcile.Result{}, err
		}

		status.Gateway =  mySubnet["gateway"]
		reqLogger.Info("Gateway set in CR", "gateway", mySubnet["gateway"])
	}

	if !reflect.DeepEqual(instance.Status, status) {
		instance.Status = status
		err := r.client.Status().Update(context.TODO(), instance)
		if err != nil {
			reqLogger.Error(err, "failed to update the NodeOverlayIp")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

//addFinalizer will add this attribute to the CR
func (r *ReconcileNodeOverlayIP) addFinalizer(m *iksv1alpha1.NodeOverlayIp) error {
    if len(m.GetFinalizers()) < 1 && m.GetDeletionTimestamp() == nil {
        m.SetFinalizers([]string{"finalizer.iks.ibm.com"})

        // Update CR
        err := r.client.Update(context.TODO(), m)
        if err != nil {
            return err
        }
    }
    return nil
}