// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type liveNetworkLoadBalancerClient struct {
	client.Client
	reader client.Reader
}

func (c liveNetworkLoadBalancerClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.reader.Get(ctx, key, obj, opts...)
}

func retryLoadBalancerConflict(writer client.Client, reader client.Reader, reconcile func(client.Client) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return reconcile(liveNetworkLoadBalancerClient{Client: writer, reader: reader})
	})
}
