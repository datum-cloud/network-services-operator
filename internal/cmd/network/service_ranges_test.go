// SPDX-License-Identifier: AGPL-3.0-only
package network

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestServiceRangeCreateAndSelectorUpdate(t *testing.T) {
	c := newTestClient(t)
	r := run(t, c, "", "service", "create", "media", "--workload", "media", "--port", "control:443", "--port", "name=media,port-range=10000-10127,protocol=UDP", "--port", "health:8080")
	require.Zero(t, r.code, r.err)
	var svc api.NetworkService
	key := client.ObjectKey{Namespace: util.ResourceNamespace, Name: "media"}
	require.NoError(t, c.Get(context.Background(), key, &svc))
	require.Equal(t, "UDP", string(svc.Spec.Ports[1].Protocol))
	require.Equal(t, int32(10127), svc.Spec.Ports[1].PortRange.End)
	r = run(t, c, "", "service", "update", "media", "--workload", "media-v2")
	require.Zero(t, r.code, r.err)
	require.NoError(t, c.Get(context.Background(), key, &svc))
	require.Equal(t, int32(10127), svc.Spec.Ports[1].PortRange.End)
	require.Equal(t, "UDP", string(svc.Spec.Ports[1].Protocol))
}
func TestServiceRejectsOverlappingRanges(t *testing.T) {
	c := newTestClient(t, &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: util.ResourceNamespace}})
	r := run(t, c, "", "service", "create", "media", "--workload", "media", "--port", "name=a,port-range=10000-10127,protocol=UDP", "--port", "name=b,port=10127,protocol=UDP")
	require.NotZero(t, r.code)
	require.Contains(t, r.err, "overlap")
}
