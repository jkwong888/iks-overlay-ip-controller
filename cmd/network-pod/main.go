package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	//corev1 "k8s.io/api/core/v1"
	"github.com/jkwong888/iks-overlay-ip-controller/pkg/apis"
	staticroute_controller "github.com/jkwong888/iks-overlay-ip-controller/pkg/controller/staticroute"
	nodeoverlayip_controller "github.com/jkwong888/iks-overlay-ip-controller/pkg/controller/nodeoverlayip-pod"

	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/operator-framework/operator-sdk/pkg/restmapper"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	"github.com/spf13/pflag"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var log = logf.Log.WithName("cmd")

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

func main() {
	// Add the zap logger flag set to the CLI. The flag set must
	// be added before calling pflag.Parse().
	pflag.CommandLine.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	pflag.Parse()

	// Use a zap logr.Logger implementation. If none of the zap
	// flags are configured (or if the zap flag set is not being
	// used), this defaults to a production zap logger.
	//
	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(zap.Logger())

	printVersion()

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, manager.Options{
		MapperProvider:     restmapper.NewDynamicRESTMapper,
	})
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	hostname := os.Getenv("NODE_HOSTNAME")
	if hostname == "" {
		panic(fmt.Errorf("Missing environment variable: NODE_HOSTNAME"))
	}

	// get the zone using the API, first get the matching Node
	c := mgr.GetClient()

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Kind:    "Node",
		Version: "v1",
	})
	err = c.Get(context.Background(), client.ObjectKey{
		Name: hostname,
	}, u)

	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	labels := u.GetLabels()
	zone := labels["failure-domain.beta.kubernetes.io/zone"]

	log.Info(fmt.Sprintf("Node Hostname: %s", hostname))
	log.Info(fmt.Sprintf("Node Zone: %s", zone))
	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	resources, err := clientset.Discovery().ServerResourcesForGroupVersion("iks.ibm.com/v1alpha1")
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	//fmt.Println("resources: %s", resources.APIResources)
	hasNodeOverlayIp := false
	for _, resource := range resources.APIResources {
		if resource.Kind != "NodeOverlayIp" {
			continue
		}

		// Start node overlay ip controller
		if err := nodeoverlayip_controller.Add(mgr, nodeoverlayip_controller.ManagerOptions{Hostname: hostname}); err != nil {
			log.Error(err, "")
			os.Exit(1)
		}
		hasNodeOverlayIp = true
		break
	}

	for _, resource := range resources.APIResources {
		if resource.Kind != "StaticRoute" {
			continue
		}

		// Start static route controller
		if err := staticroute_controller.Add(mgr, staticroute_controller.ManagerOptions{
				Hostname: hostname,
				Zone: zone, 
				HasNodeOverlayIpCR: hasNodeOverlayIp,
			}); err != nil {
			log.Error(err, "")
			os.Exit(1)
		}
		break
	}

	log.Info("Starting the Cmd.")
	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited non-zero")
		os.Exit(1)
	}
}
