package staticroute

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jkwong888/iks-overlay-ip-controller/pkg/util"
	iksv1alpha1 "github.com/jkwong888/iks-overlay-ip-controller/pkg/apis/iks/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_staticroute")

type ManagerOptions struct {
	Hostname string
	Zone string
	HasNodeOverlayIpCR bool
}

// Add creates a new StaticRoute Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, options ManagerOptions) error {
	return add(mgr, newReconciler(mgr, options))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, options ManagerOptions) reconcile.Reconciler {
	return &ReconcileStaticRoute{client: mgr.GetClient(), scheme: mgr.GetScheme(), options: options}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("staticroute-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource StaticRoute
	err = c.Watch(&source.Kind{Type: &iksv1alpha1.StaticRoute{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileStaticRoute implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileStaticRoute{}

// ReconcileStaticRoute reconciles a Node object
type ReconcileStaticRoute struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
	options ManagerOptions
}

// Reconcile reads that state of the cluster for a StaticRoute object and makes changes based on the state read
// and what is in the StaticRoute.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileStaticRoute) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Name", request.Name)

	reqLogger.Info("Reconciling StaticRoute")

	// Fetch the StaticRoute instance
	instance := &iksv1alpha1.StaticRoute{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Add finalizer for this CR
	reqLogger.Info("Adding Finalizer for the StaticRoute")
	if err := r.addFinalizer(instance); err != nil {
		reqLogger.Error(err, "Failed to update StaticRoute with finalizer")
		return reconcile.Result{}, err
	}

	isDeleted := instance.GetDeletionTimestamp() != nil
	if isDeleted {
		// handle finalizer -- first delete static route
		err := delStaticRoute(instance.Spec.Subnet)
		if err != nil {
			return reconcile.Result{}, err
		}

		if len(instance.Status.NodeStatus) > 0 {
			// remove myself from the status list
			removeFromStatus(instance, r.options.Hostname)

			// Update CR Status
			reqLogger.Info("Updating status for StaticRoute", "status", instance.Status)
			err = r.client.Status().Update(context.TODO(), instance)
			if err != nil {
				return reconcile.Result{}, err
			}

			// requeue this immediately -- once status is empty we can clear finalizers and remove CR
			return reconcile.Result{}, nil
		}

		// we can clear the finalizer if we are the last instance to remove the route; this
		// tells kube that it's safe to remove the resource
		reqLogger.Info("Removing finalizer for StaticRoute")
		instance.SetFinalizers(nil)
		err = r.client.Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	zoneVal := instance.GetLabels()["failure-domain.beta.kubernetes.io/zone"]
	if zoneVal != "" && zoneVal != r.options.Zone {
		// a zone is specified and the route is not for this zone, ignore
		reqLogger.Info("Ignoring, zone does not match", "NodeZone", r.options.Zone, "CRZone", zoneVal)
		return reconcile.Result{}, nil
	}

	gateway := instance.Spec.Gateway
	if gateway == "" && r.options.HasNodeOverlayIpCR {
		// if the NodeOverlayIp CR is available, we can query this node's IP and possibly get its gateway
		nodeOverlayIp := &iksv1alpha1.NodeOverlayIp{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: r.options.Hostname}, nodeOverlayIp)
		if err != nil {
			// this isn't necessarily a fatal error, requeue the static route and try again later
			reqLogger.Info("No NodeOverlayIp exists for node, requeuing", "node", r.options.Hostname)
			return reconcile.Result{Requeue: true}, nil
		}

		// the NodeOverlayIp has an optional Gateway in the spec, grab this if it exists
		gateway = nodeOverlayIp.Status.Gateway
		if gateway == "" {
			// gateway may not be set yet, requeue immediately
			reqLogger.Info("NodeOverlayIp has no gateway in status yet, requeuing", "node", r.options.Hostname)
			return reconcile.Result{Requeue: true}, nil
		}

		reqLogger.Info("NodeOverlayIp for node has gateway", "node", r.options.Hostname, "gateway", gateway)
	}

	// note that if "gateway" is still empty, we'll create the route through the default private network gateway
	if gateway == "" {
		gateway, err = getFallbackGateway()
		if err != nil {
			reqLogger.Info("Unable to retrieve fallback gateway")
			return reconcile.Result{}, err 
		}
	}

	err = addStaticRoute(instance.Spec.Subnet, gateway)
	if err != nil {
		return reconcile.Result{}, err
	}

	device, err := getRouteDevice(instance.Spec.Subnet)
	if err != nil {
		return reconcile.Result{}, err
	}

	addToStatus(instance, r.options.Hostname, device, gateway)

	reqLogger.Info("Update the StaticRoute status", "staticroute", instance)
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		reqLogger.Error(err, "failed to update the staticroute")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func addToStatus(m *iksv1alpha1.StaticRoute, hostname string, device string, gateway string) {
	// Update the status if necessary
	foundStatus := false
	for _, val := range m.Status.NodeStatus {
		if val.Hostname != hostname {
			continue
		}

		val.Gateway = gateway
		val.Device = device
		foundStatus = true
		break
	}
	
	if !foundStatus {
		m.Status.NodeStatus = append(m.Status.NodeStatus, iksv1alpha1.StaticRouteNodeStatus{
			Hostname: hostname,
			Gateway: gateway,
			Device: device,
		})
	}
}

func removeFromStatus(m *iksv1alpha1.StaticRoute, hostname string) {
	// Update the status if necessary
	statusArr := []iksv1alpha1.StaticRouteNodeStatus{}
	for _, val := range m.Status.NodeStatus {
		valCopy := val.DeepCopy()

		if valCopy.Hostname == hostname {
			// don't append myself
			continue
		}

		statusArr = append(statusArr, *valCopy)
	}
	
	newStatus := iksv1alpha1.StaticRouteStatus{
		NodeStatus: statusArr,
	}

	m.Status = newStatus
}

//addFinalizer will add this attribute to the CR
func (r *ReconcileStaticRoute) addFinalizer(m *iksv1alpha1.StaticRoute) error {
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

func getRouteDevice(subnet string) (string, error) {
	// find the device that the subnet is being routed through
	re := regexp.MustCompile(`(?s).*dev ([^\s]*) .*`)
	out, _, err := util.ExecIpCmd(fmt.Sprintf("route get %s", subnet))
	if err != nil {
		return "", err
	}

	device := re.ReplaceAllString(out, "$1")

	return device, nil
}

func getFallbackGateway() (string, error) {
	// if no gateway is defined, just use the same gateway that 10.0.0.0/8 uses and assume
	// that the edge device will route/NAT us to the network
	re := regexp.MustCompile(`(?s).*via ([^\s]*) .*`)
	out, _, err := util.ExecIpCmd(fmt.Sprintf("route get 10.0.0.0/8"))
	if err != nil {
		return "", err
	}

	myGateway := re.ReplaceAllString(out, "$1")

	return myGateway, nil
}

func addStaticRoute(subnet string, gateway string) (error) {
	// check if route already exists
	out, code, err := util.ExecIpCmd(fmt.Sprintf("route show %s", subnet))
	if err != nil {
		return err
	}

	re := regexp.MustCompile(`(?s).*via ([^\s]*) .*`)

	// if route exists already
	if out != "" {
		currGateway := re.ReplaceAllString(out, "$1")

		if currGateway == gateway {
			log.Info(fmt.Sprintf("Route for %s via %s already exists", subnet, gateway))
			return nil
		}

		// delete the route if the gateway doesn't match
		out, code, err := util.ExecIpCmd(fmt.Sprintf("route del %s via %s", subnet, currGateway))
		if err != nil {
			return err
		}

		if code != 0 {
			return fmt.Errorf("Error executing \"ip route del\", output: %s", out)
		}
	}

	// add the new route
	out, code, err = util.ExecIpCmd(fmt.Sprintf("route add %s via %s", subnet, gateway))
	if err != nil {
		return err
	}

	if code != 0 {
		return fmt.Errorf("Error executing \"ip route add\", output: %s", out)
	}

	return nil
}

func delStaticRoute(subnet string) (error) {
	// check if route already exists
	out, code, err := util.ExecIpCmd(fmt.Sprintf("route show %s", subnet))
	if err != nil {
		return err
	}

	// if route already doesn't exists
	if out == "" {
		log.Info(fmt.Sprintf("Route for %s already doesn't exist", subnet))
		return nil
	}

	// delete the route 
	out, code, err = util.ExecIpCmd(fmt.Sprintf("route del %s", strings.TrimSuffix(out, " \n")))
	if err != nil {
		return err
	}

	if code != 0 {
		return fmt.Errorf("Error executing \"ip route del\", output: %s", out)
	}

	return nil
}