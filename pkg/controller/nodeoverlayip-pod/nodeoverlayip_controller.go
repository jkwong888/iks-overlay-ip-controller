package nodeoverlayip

import (
	"context"
	"regexp"
	"strings"
	"fmt"
	"os"
	"reflect"

	iksv1alpha1 "github.com/jkwong888/iks-overlay-ip-controller/pkg/apis/iks/v1alpha1"
	 "github.com/jkwong888/iks-overlay-ip-controller/pkg/util"
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

type ManagerOptions struct {
	Hostname string
}

// Add creates a new NodeOverlayIP Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, options ManagerOptions) error {
	return add(mgr, newReconciler(mgr, options))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, options ManagerOptions) reconcile.Reconciler {
	return &ReconcileNodeOverlayIP{client: mgr.GetClient(), scheme: mgr.GetScheme(), options: options}
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
	options ManagerOptions
}

// Reconcile reads that state of the cluster for a NodeOverlayIP object and makes changes based on the state read
// and what is in the NodeOverlayIP.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileNodeOverlayIP) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Name", request.Name)

	reqLogger.Info("Reconciling NodeOverlayIp")

	intf := os.Getenv("INTERFACE")
	intfLabel := os.Getenv("INTERFACE_LABEL")

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

	if instance.GetName() != r.options.Hostname {
		// this IP doesn't belong to us, so just ignore it
		reqLogger.Info("NodeOverlayIp doesn't match hostname, ignoring", "Hostname", r.options.Hostname)
		return reconcile.Result{}, nil
	}

	// someone deleted the IP manually, but i'm still alive, so remove the device
	isDeleted := instance.GetDeletionTimestamp() != nil
	if isDeleted {
		// remove the IP
		if instance.Status.InterfaceLabel != "" {
			err := delOverlayDevice(instance.Status.InterfaceLabel)
			if err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// actually create the node device according to the CR
	err = addOverlayDevice(intf, intfLabel)
	if err != nil {
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// add the node IP according to the CR
	err = addOverlayIp(intfLabel, instance.Status.IpAddr)
	if err != nil {
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Update the status if necessary
	status := iksv1alpha1.NodeOverlayIpStatus{
		Interface: intf,
		InterfaceLabel: intfLabel,
	}

	if !reflect.DeepEqual(instance.Status, status) {
		instance.Status = status
		err := r.client.Update(context.TODO(), instance)
		if err != nil {
			reqLogger.Error(err, "failed to update the NodeOverlayIp")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func delOverlayDevice(label string) (error) {
	// check if overlay device still exists
	out, code, err := util.ExecIpCmd(fmt.Sprintf("link show %s", label))
	if err != nil {
		return err
	}

	// if device doesn't exist, create macvlan device
	if code != 0 {
		if strings.Contains(out, "does not exist") {
			log.Info(fmt.Sprintf("Device %s is already deleted", label))
			return nil
		}
	} else {
		return fmt.Errorf("Error executing \"ip link show\", output: %s", out)
	}

	log.Info(fmt.Sprintf("Deleting device %s", label))
	out, code, err = util.ExecIpCmd(fmt.Sprintf("link del %s", label))
	if err != nil {
		return err
	}

	return nil
}

func addOverlayDevice(device string, label string) (error) {
	// check if overlay device already exists
	out, code, err := util.ExecIpCmd(fmt.Sprintf("link show %s", label))
	if err != nil {
		return err
	}

	// if device doesn't exist, create macvlan device
	if code != 0 {
		if strings.Contains(out, "does not exist") {
			out, code, err := util.ExecIpCmd(fmt.Sprintf("link add %s link %s type macvlan", label, device))
			if err != nil {
				return err
			}

			if code != 0 {
				return fmt.Errorf("Error executing \"ip link add\", output: %s", out)
			}

			// now check if overlay device exists
			out, code, err = util.ExecIpCmd(fmt.Sprintf("link show %s", label))
			if code != 0 {
				return fmt.Errorf("Error executing \"ip link show\", output: %s", out)
			} else if err != nil {
				return err
			}
		} else {
			// some other error
			return fmt.Errorf("Error executing \"ip link show\", output: %s", out)
		}
	}

	// if device isn't up, create macvlan device
	re := regexp.MustCompile(`(?s).*state (UP|DOWN).*`)
	linkState := re.ReplaceAllString(out, "$1")
	if linkState != "DOWN" {
		log.Info(fmt.Sprintf("Device %s is already up", label))
		return nil
	}

	out, code, err = util.ExecIpCmd(fmt.Sprintf("link set %s up", label))
	if err != nil {
		return err
	}

	if code != 0 {
		return fmt.Errorf("Error executing \"ip link set\", output: %s", out)
	}

	return nil
}

func addOverlayIp(device string, ipAddr string) (error) {
	// check if overlay ip already exists
	out, code, err := util.ExecIpCmd(fmt.Sprintf("addr show %s", device))
	if err != nil {
		return err
	}

	if code != 0 {
		// some other error
		return fmt.Errorf("Error executing \"ip addr show\", output: %s", out)
	}

	// get the existing IP
	currIP := ""
	re := regexp.MustCompile(`(?s).*inet ([^\s]*) .*`)
	if re.MatchString(out) {
		currIP = re.ReplaceAllString(out, "$1")
		if currIP == ipAddr {
			log.Info(fmt.Sprintf("IP %s is already set on device %s", ipAddr, device))
			return nil
		}
	}
	
	// an existing IP (not the real IP) is already there, delete the bad IP
	if currIP != "" {
		log.Info(fmt.Sprintf("IP addr %s is currently set on device %s, removing ...", currIP, device))
		out, code, err = util.ExecIpCmd(fmt.Sprintf("addr del %s dev %s", currIP, device))
		if err != nil {
			return err
		}

		if code != 0 {
			return fmt.Errorf("Error executing \"ip addr del\", output: %s", out)
		}
	}

	// add IP
	out, code, err = util.ExecIpCmd(fmt.Sprintf("addr add %s dev %s", ipAddr, device))
	if err != nil {
		return err
	}

	if code != 0 {
		return fmt.Errorf("Error executing \"ip addr del\", output: %s", out)
	}
	
	return  nil
}

