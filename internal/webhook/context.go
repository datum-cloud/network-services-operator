package webhook

import (
	"context"
)

type ClusterContextKey struct{}

func WithClusterName(ctx context.Context, clusterName string) context.Context {
	return context.WithValue(ctx, ClusterContextKey{}, clusterName)
}

func ClusterNameFromContext(ctx context.Context) string {
	if clusterName, ok := ctx.Value(ClusterContextKey{}).(string); ok {
		return clusterName
	}
	return ""
}
