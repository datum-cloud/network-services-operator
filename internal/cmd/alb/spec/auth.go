// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
)

type BasicAuthUser struct {
	Username string
	Password string
}

const (
	httpRouteKind = "HTTPRoute"

	BasicAuthAccepted    = "accepted"
	BasicAuthNotAccepted = "not accepted"
	BasicAuthPending     = "pending"
)

type BasicAuthAttachment struct {
	Policy     string
	SecretName string
	AllRoutes  bool
	Rules      []string
	Listeners  []string
	State      string
	Message    string
}

func (a BasicAuthAttachment) Scope() string {
	if a.AllRoutes {
		return "all"
	}
	parts := append([]string{}, a.Rules...)
	for _, l := range a.Listeners {
		parts = append(parts, "listener "+l)
	}
	return strings.Join(parts, ", ")
}

func (a BasicAuthAttachment) StateDetail() string {
	if a.State == BasicAuthNotAccepted && a.Message != "" {
		return a.State + ": " + a.Message
	}
	return a.State
}

func BasicAuthAttachments(proxyName string, policies []envoygatewayv1alpha1.SecurityPolicy) []BasicAuthAttachment {
	var out []BasicAuthAttachment
	for i := range policies {
		policy := &policies[i]
		if policy.Spec.BasicAuth == nil {
			continue
		}
		attachment, ok := basicAuthAttachment(proxyName, policy)
		if ok {
			out = append(out, attachment)
		}
	}
	return out
}

func basicAuthAttachment(proxyName string, policy *envoygatewayv1alpha1.SecurityPolicy) (BasicAuthAttachment, bool) {
	a := BasicAuthAttachment{Policy: policy.Name, SecretName: string(policy.Spec.BasicAuth.Users.Name)}
	matched := false
	for _, ref := range policy.Spec.TargetRefs {
		if string(ref.Group) != gatewayv1.GroupName || string(ref.Name) != proxyName {
			continue
		}
		kind := string(ref.Kind)
		if kind != gatewayKind && kind != httpRouteKind {
			continue
		}
		matched = true
		switch {
		case ref.SectionName == nil:
			a.AllRoutes = true
		case kind == httpRouteKind:
			a.Rules = appendUnique(a.Rules, string(*ref.SectionName))
		default:
			a.Listeners = appendUnique(a.Listeners, string(*ref.SectionName))
		}
	}
	if !matched {
		return a, false
	}
	if a.AllRoutes {
		a.Rules, a.Listeners = nil, nil
	}
	a.State, a.Message = basicAuthState(policy)
	return a, true
}

func basicAuthState(policy *envoygatewayv1alpha1.SecurityPolicy) (string, string) {
	state := BasicAuthPending
	for _, ancestor := range policy.Status.Ancestors {
		cond := apimeta.FindStatusCondition(ancestor.Conditions, string(gatewayv1.PolicyConditionAccepted))
		if cond == nil {
			continue
		}
		switch cond.Status {
		case metav1.ConditionFalse:
			return BasicAuthNotAccepted, cond.Message
		case metav1.ConditionTrue:
			state = BasicAuthAccepted
		}
	}
	return state, ""
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
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
