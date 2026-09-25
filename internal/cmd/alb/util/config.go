// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	resourceManagerGroup   = "resourcemanager.miloapis.com"
	resourceManagerVersion = "v1alpha1"

	ResourceNamespace = "default"
	FieldManager      = "datumctl-alb"
	CAFileEnv         = "DATUM_CA_FILE"
	RequestTimeout    = 30 * time.Second
)

func ProjectControlPlaneURL(apiHost, projectID string) string {
	return fmt.Sprintf("https://%s/apis/%s/%s/projects/%s/control-plane",
		apiHost, resourceManagerGroup, resourceManagerVersion, projectID)
}

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering networking scheme: %w", err)
	}
	if err := envoygatewayv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering envoy gateway scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering core scheme: %w", err)
	}
	return scheme, nil
}

func UserAgent() string {
	return "datumctl-alb"
}
