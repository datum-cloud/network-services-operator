// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/datumctl/plugin"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	resourceManagerGroup   = "resourcemanager.miloapis.com"
	resourceManagerVersion = "v1alpha1"

	ResourceNamespace = "default"
	CAFileEnv         = "DATUM_CA_FILE"
	RequestTimeout    = 30 * time.Second
)

func ProjectControlPlaneURL(apiHost, projectID string) string {
	return fmt.Sprintf("https://%s/apis/%s/%s/projects/%s/control-plane",
		apiHost, resourceManagerGroup, resourceManagerVersion, projectID)
}

func RestConfig(project string) (*rest.Config, error) {
	if project == "" {
		return nil, NewCLIError(ExitUsage, "no project set").
			WithFix("pass --project, or set a default with:\n       datumctl config set project <name>")
	}

	pctx := plugin.Context()
	if pctx.APIHost == "" {
		return nil, NewCLIError(ExitUnavailable, "DATUM_API_HOST is not set").
			WithFix("run this through datumctl:\n       datumctl network ...")
	}

	token, err := plugin.Token()
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("getting credentials: %v", err)).
			WithFix("re-run `datumctl login` and try again.").
			WithCause(err)
	}

	return &rest.Config{
		Host:            ProjectControlPlaneURL(pctx.APIHost, project),
		BearerToken:     token,
		UserAgent:       "datumctl-network",
		TLSClientConfig: rest.TLSClientConfig{CAFile: os.Getenv(CAFileEnv)},
		Timeout:         RequestTimeout,
	}, nil
}

func NewClient(project string) (client.Client, error) {
	cfg, err := RestConfig(project)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering networking scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("building API client: %v", err)).WithCause(err)
	}
	return c, nil
}

func ProjectFromCmd(cmd *cobra.Command) string {
	project, _ := cmd.Root().PersistentFlags().GetString("project")
	return project
}
