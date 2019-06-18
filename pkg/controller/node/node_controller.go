package node

import (
	"context"

	iksv1alpha1 "github.com/jkwong888/iks-overlay-ip-controller/pkg/apis/iks/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_node")

// Add creates a new Node Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileNode{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("node-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Node
	err = c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to nodeoverlayips and requeue the owner Node
	err = c.Watch(&source.Kind{Type: &iksv1alpha1.NodeOverlayIp{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &corev1.Node{},
	})
	if err != nil {
		return err
	}

	// Watch for changes to static routes and requeue the owner Node
	err = c.Watch(&source.Kind{Type: &iksv1alpha1.StaticRoute{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &corev1.Node{},
	})
	if err != nil {
		return err
	}


	return nil
}

// blank assignment to verify that ReconcileNode implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileNode{}

// ReconcileNode reconciles a Node object
type ReconcileNode struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Node object and makes changes based on the state read
// and what is in the Node.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileNode) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Node")

	// Fetch the Node instance
	instance := &corev1.Node{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		// TODO: use a finalizer to cleanup IPAM

		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Check if an IP already exists
	found := &iksv1alpha1.NodeOverlayIp{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: instance.Name}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new NodeOverlayIp object
		nodeOverlayIP, err := newNodeOverlayIP(instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		reqLogger.Info("Creating a new NodeOverlayIp", "node", instance.Name)
		err = r.client.Create(context.TODO(), nodeOverlayIP)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Set Node instance as the owner and controller
		if err := controllerutil.SetControllerReference(instance, nodeOverlayIP, r.scheme); err != nil {
			return reconcile.Result{}, err
		}

		// created successfully - requeue to see if we need a static route
		return reconcile.Result{Requeue: true}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	} else {
		reqLogger.Info("NodeOverlayIp already exists", "NodeOverlayIp.Name", found.Name)
	}

	return reconcile.Result{}, nil
}

// newNodeOverlayIP asks IPAM for an IP and returns a CR. TODO: return nil if no IPAM is configured
func newNodeOverlayIP(cr *corev1.Node) (*iksv1alpha1.NodeOverlayIp, error) {
	zone := cr.GetLabels()["failure-domain.beta.kubernetes.io/zone"]

	labels := map[string]string{
		"node":   cr.Name,
		"region": cr.GetLabels()["failure-domain.beta.kubernetes.io/region"],
		"zone":   zone,
	}

	return &iksv1alpha1.NodeOverlayIp{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cr.Name,
			Labels: labels,
		},
	}, nil
}