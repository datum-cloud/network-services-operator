// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"flag"
	"fmt"
	"os"

	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/publicudp"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func run() error {
	var projectConfig, cellConfig, project, location, fabric, namespace, metrics, health string
	flag.StringVar(&projectConfig, "project-kubeconfig", "", "project control plane kubeconfig")
	flag.StringVar(&cellConfig, "cell-kubeconfig", "", "cell kubeconfig; empty uses in-cluster credentials")
	flag.StringVar(&project, "project", "", "project identity")
	flag.StringVar(&location, "location", "", "cell location")
	flag.StringVar(&fabric, "fabric-namespace", "galactic-system", "native fabric namespace")
	flag.StringVar(&namespace, "namespace", "default", "project namespace to watch")
	flag.StringVar(&metrics, "metrics-bind-address", "127.0.0.1:9082", "metrics listen address")
	flag.StringVar(&health, "health-probe-bind-address", ":9083", "readiness listen address")
	flag.Parse()
	if project == "" || location == "" || projectConfig == "" || namespace == "" || fabric == "" {
		return fmt.Errorf("project, location, namespace, fabric-namespace and project-kubeconfig are required")
	}
	scheme := runtime.NewScheme()
	_ = core.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	pc, err := clientcmd.BuildConfigFromFlags("", projectConfig)
	if err != nil {
		return err
	}
	cc, err := clientcmd.BuildConfigFromFlags("", cellConfig)
	if err != nil {
		return err
	}
	cell, err := client.New(cc, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	m, err := ctrl.NewManager(pc, ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          true,
		LeaderElectionID:        "public-udp-" + location,
		LeaderElectionNamespace: namespace,
		Cache:                   cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}},
		Metrics:                 server.Options{BindAddress: metrics},
		HealthProbeBindAddress:  health,
	})
	if err != nil {
		return err
	}
	projectClient, err := client.New(pc, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	r := &publicudp.Reconciler{
		Project: projectClient, Cell: cell, ProjectName: project,
		Location: location, FabricNamespace: fabric,
	}
	if err := ctrl.NewControllerManagedBy(m).For(&api.NetworkService{}).Complete(r); err != nil {
		return err
	}
	if err := m.AddReadyzCheck("ready", healthz.Ping); err != nil {
		return err
	}
	return m.Start(ctrl.SetupSignalHandler())
}
func main() {
	ctrl.SetLogger(zap.New())
	if err := run(); err != nil {
		ctrl.Log.Error(err, "public UDP controller failed")
		os.Exit(1)
	}
}
