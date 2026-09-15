// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func NewClient(project string) (client.Client, error) {
	if project == "" {
		return nil, NewCLIError(ExitUsage, "no project set").
			WithFix("pass --project, or set a default with:\n       datumctl config set project <name>")
	}

	ctx := plugin.Context()
	if ctx.APIHost == "" {
		return nil, NewCLIError(ExitUnavailable, "DATUM_API_HOST is not set").
			WithFix("run this through datumctl:\n       datumctl alb ...")
	}

	token, err := plugin.Token()
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("getting credentials: %v", err)).
			WithFix("re-run `datumctl login` and try again.").
			WithCause(err)
	}

	scheme, err := NewScheme()
	if err != nil {
		return nil, err
	}

	cfg := &rest.Config{
		Host:            ProjectControlPlaneURL(ctx.APIHost, project),
		BearerToken:     token,
		UserAgent:       UserAgent(),
		TLSClientConfig: tlsClientConfig(),
		Timeout:         RequestTimeout,
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("building API client: %v", err)).WithCause(err)
	}
	return c, nil
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

func tlsClientConfig() rest.TLSClientConfig {
	return rest.TLSClientConfig{CAFile: os.Getenv(CAFileEnv)}
}

func UserAgent() string {
	return "datumctl-alb"
}

func ProjectFromCmd(cmd *cobra.Command) string {
	project, _ := cmd.Root().PersistentFlags().GetString("project")
	return project
}

func OrgFromCmd(cmd *cobra.Command) string {
	org, _ := cmd.Root().PersistentFlags().GetString("org")
	return org
}
