// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
)

type BasicAuthUser struct {
	Username string
	Password string
}

func BasicAuthSecretName(proxyName string) string {
	return proxyName + "-basic-auth"
}

func BuildSecurityPolicy(proxyName, displayName string) *envoygatewayv1alpha1.SecurityPolicy {
	policy := &envoygatewayv1alpha1.SecurityPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: envoygatewayv1alpha1.GroupVersion.String(),
			Kind:       envoygatewayv1alpha1.KindSecurityPolicy,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyName,
			Namespace: util.ResourceNamespace,
		},
		Spec: envoygatewayv1alpha1.SecurityPolicySpec{
			PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{
				TargetRefs: []gatewayv1.LocalPolicyTargetReferenceWithSectionName{{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Group: gatewayv1.GroupName,
						Kind:  gatewayv1.Kind(gatewayKind),
						Name:  gatewayv1.ObjectName(proxyName),
					},
				}},
			},
			BasicAuth: &envoygatewayv1alpha1.BasicAuth{
				Users: gatewayv1.SecretObjectReference{
					Name: gatewayv1.ObjectName(BasicAuthSecretName(proxyName)),
				},
			},
		},
	}
	if displayName != "" {
		policy.Annotations = map[string]string{
			display.AnnotationDisplayName: displayName,
		}
	}
	return policy
}

func BuildBasicAuthSecret(proxyName string, users []BasicAuthUser) (*corev1.Secret, error) {
	content, err := GenerateHtpasswd(users)
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      BasicAuthSecretName(proxyName),
			Namespace: util.ResourceNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			envoygatewayv1alpha1.BasicAuthUsersSecretKey: []byte(content),
		},
	}, nil
}

func GenerateHtpasswd(users []BasicAuthUser) (string, error) {
	if len(users) == 0 {
		return "", util.UsageErrorf("at least one user is required")
	}
	lines := make([]string, 0, len(users))
	seen := map[string]struct{}{}
	for _, user := range users {
		username := strings.TrimSpace(user.Username)
		if username == "" {
			return "", util.UsageErrorf("username is required")
		}
		if strings.ContainsAny(username, ":\n") {
			return "", util.UsageErrorf("username must not contain ':' or newlines")
		}
		if user.Password == "" {
			return "", util.UsageErrorf("password is required for user %q", username)
		}
		if _, dup := seen[username]; dup {
			return "", util.UsageErrorf("duplicate username %q", username)
		}
		seen[username] = struct{}{}
		sum := sha1.Sum([]byte(user.Password))
		lines = append(lines, fmt.Sprintf("%s:{SHA}%s", username, base64.StdEncoding.EncodeToString(sum[:])))
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func ParseHtpasswdUsernames(secret *corev1.Secret) []string {
	if secret == nil {
		return nil
	}
	content := secret.Data[envoygatewayv1alpha1.BasicAuthUsersSecretKey]
	if len(content) == 0 {
		content = []byte(secret.StringData[envoygatewayv1alpha1.BasicAuthUsersSecretKey])
	}
	var usernames []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		username, _, ok := strings.Cut(line, ":")
		if !ok || username == "" {
			continue
		}
		usernames = append(usernames, username)
	}
	return usernames
}
