// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const CompletionTimeout = 3 * time.Second

func CompleteALBNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return withCompletionDeadline(func() []string {
		c, err := NewClient(ProjectFromCmd(cmd))
		if err != nil {
			return nil
		}

		var list networkingv1alpha.HTTPProxyList
		if err := c.List(cmd.Context(), &list, client.InNamespace(ResourceNamespace)); err != nil {
			return nil
		}

		names := make([]string, 0, len(list.Items))
		for i := range list.Items {
			names = append(names, list.Items[i].Name)
		}
		return names
	})
}

func CompleteNetworkServiceNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return withCompletionDeadline(func() []string {
		c, err := NewClient(ProjectFromCmd(cmd))
		if err != nil {
			return nil
		}

		var list networkingv1alpha.NetworkServiceList
		if err := c.List(cmd.Context(), &list, client.InNamespace(ResourceNamespace)); err != nil {
			return nil
		}

		names := make([]string, 0, len(list.Items))
		for i := range list.Items {
			names = append(names, list.Items[i].Name)
		}
		return names
	})
}

func CompleteNetworkServicePorts(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	services, _ := cmd.Flags().GetStringArray("network-service")
	if len(services) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	serviceName := services[len(services)-1]

	return withCompletionDeadline(func() []string {
		c, err := NewClient(ProjectFromCmd(cmd))
		if err != nil {
			return nil
		}

		service := &networkingv1alpha.NetworkService{}
		key := types.NamespacedName{Namespace: ResourceNamespace, Name: serviceName}
		if err := c.Get(cmd.Context(), key, service); err != nil {
			return nil
		}

		ports := make([]string, 0, len(service.Spec.Ports))
		for _, p := range service.Spec.Ports {
			ports = append(ports, p.Name)
		}
		return ports
	})
}

func withCompletionDeadline(fn func() []string) ([]string, cobra.ShellCompDirective) {
	done := make(chan []string, 1)
	go func() { done <- fn() }()

	timer := time.NewTimer(CompletionTimeout)
	defer timer.Stop()

	select {
	case names := <-done:
		return names, cobra.ShellCompDirectiveNoFileComp
	case <-timer.C:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func CompleteEnum(allowed ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return allowed, cobra.ShellCompDirectiveNoFileComp
	}
}
