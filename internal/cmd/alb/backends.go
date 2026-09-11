// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"context"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func addBackendFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("endpoint", nil, "Origin URL, http or https (repeatable)")
	cmd.Flags().StringArray("network-service", nil, "Existing NetworkService to send traffic to (repeatable, pair with --port)")
	cmd.Flags().StringArray("port", nil, "Named port on the preceding --network-service")
	cmd.Flags().String("tls-hostname", "", "Hostname used to verify TLS when an --endpoint origin is an IP")

	_ = cmd.RegisterFlagCompletionFunc("network-service", util.CompleteNetworkServiceNames)
	_ = cmd.RegisterFlagCompletionFunc("port", util.CompleteNetworkServicePorts)
}

func backendFlagsChanged(cmd *cobra.Command) bool {
	for _, name := range []string{"endpoint", "network-service", "port", "tls-hostname"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func backendsFromFlags(cmd *cobra.Command) ([]spec.BackendInput, error) {
	endpoints, _ := cmd.Flags().GetStringArray("endpoint")
	services, _ := cmd.Flags().GetStringArray("network-service")
	ports, _ := cmd.Flags().GetStringArray("port")
	tlsHostname, _ := cmd.Flags().GetString("tls-hostname")

	return spec.ParseBackendFlags(spec.BackendFlags{
		Endpoints:       endpoints,
		NetworkServices: services,
		Ports:           ports,
		TLSHostname:     tlsHostname,
	})
}

func singleBackendFromFlags(cmd *cobra.Command) (spec.BackendInput, error) {
	backends, err := backendsFromFlags(cmd)
	if err != nil {
		return spec.BackendInput{}, err
	}
	if len(backends) != 1 {
		return spec.BackendInput{}, util.UsageErrorf("name exactly one backend").
			WithFix("pass either --endpoint URL or --network-service NAME --port PORTNAME")
	}
	return backends[0], nil
}

func ensureNetworkServices(ctx context.Context, c client.Client, backends []spec.BackendInput) error {
	for _, b := range backends {
		if b.NetworkService == "" {
			continue
		}
		service, err := util.GetNetworkService(ctx, c, b.NetworkService)
		if err != nil {
			return err
		}
		if err := util.EnsureNetworkServicePort(service, b.Port); err != nil {
			return err
		}
	}
	return nil
}
