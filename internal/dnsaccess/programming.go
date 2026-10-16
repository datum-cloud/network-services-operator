// Copyright 2026 Datum Cloud, Inc.
// SPDX-License-Identifier: AGPL-3.0-or-later

package dnsaccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	cloudv1alpha1 "go.datum.net/cloud/api/v1alpha1"
	networkv1alpha1 "go.datum.net/network/api/v1alpha1"
)

// ProgrammingRequirements fences readiness to the complete service topology.
type ProgrammingRequirements struct {
	InputDigest string
	Nodes       []string
}

// ProgrammingPlan returns the exact digest and node set Galactic must report
// before network services publishes resolver settings.
func ProgrammingPlan(
	policy *networkv1alpha1.ServiceRoutePolicy,
	endpoint *networkv1alpha1.ServiceEndpoint,
	attachments []*cloudv1alpha1.VPCAttachment,
	consumerVPCIdentity string,
) (ProgrammingRequirements, error) {
	plan := ProgrammingRequirements{}
	if policy == nil || endpoint == nil {
		return plan, errors.New("policy and endpoint are required")
	}
	if policy.Spec.Frontend != nil && (policy.UID == "" || endpoint.UID == "" || consumerVPCIdentity == "") {
		return plan, errors.New("translated programming requires current policy, endpoint, and VPC identities")
	}
	selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.AttachmentSelector)
	if err != nil {
		return plan, err
	}
	producers, err := producerCandidates(endpoint, attachments)
	if err != nil {
		return plan, err
	}
	if err := validateProducerTopology(endpoint, producers); err != nil {
		return plan, err
	}
	ready := map[types.NamespacedName]bool{}
	for _, attachment := range readyAttachments(attachments) {
		ready[types.NamespacedName{Namespace: attachment.Namespace, Name: attachment.Name}] =
			attachment.Status.HostInterface != ""
	}
	relevant := map[types.NamespacedName]bool{}
	for _, producer := range producers {
		relevant[types.NamespacedName{Namespace: producer.Namespace, Name: producer.Name}] = true
	}
	nodes := map[string]bool{}
	selected := 0
	sorted := nonNilAttachments(attachments)
	for _, attachment := range sorted {
		if !selector.Matches(labels.Set(attachment.Labels)) || !consumerMatchesVPC(policy, attachment, consumerVPCIdentity) {
			continue
		}
		key := types.NamespacedName{Namespace: attachment.Namespace, Name: attachment.Name}
		relevant[key] = true
		selected++
		if attachment.DeletionTimestamp != nil || !ready[key] {
			return plan, fmt.Errorf("consumer attachment %s is not ready", key)
		}
		producer := chooseProducer(endpoint.Spec.DeliveryMode, attachment.Status.Node, producers)
		if producer == nil {
			return plan, fmt.Errorf("consumer attachment %s has no eligible producer", key)
		}
		if policy.Spec.Frontend != nil && (attachment.UID == "" || producer.UID == "") {
			return plan, errors.New("translated programming requires current attachment UIDs")
		}
		nodes[attachment.Status.Node] = true
		nodes[producer.Status.Node] = true
	}
	if selected == 0 {
		return plan, errors.New("no eligible consumer attachments")
	}

	snapshot := struct {
		PolicyUID          types.UID
		PolicyGeneration   int64
		PolicySpec         networkv1alpha1.ServiceRoutePolicySpec
		EndpointUID        types.UID
		EndpointGeneration int64
		EndpointSpec       networkv1alpha1.ServiceEndpointSpec
		VPCIdentity        string
		Attachments        []any
	}{
		PolicyUID: policy.UID, PolicyGeneration: policy.Generation, PolicySpec: policy.Spec,
		EndpointUID: endpoint.UID, EndpointGeneration: endpoint.Generation, EndpointSpec: endpoint.Spec,
		VPCIdentity: consumerVPCIdentity,
	}
	for _, attachment := range sorted {
		key := types.NamespacedName{Namespace: attachment.Namespace, Name: attachment.Name}
		if !relevant[key] {
			continue
		}
		snapshot.Attachments = append(snapshot.Attachments, struct {
			Namespace, Name string
			UID             types.UID
			Generation      int64
			Labels          map[string]string
			Deleted         bool
			Spec            cloudv1alpha1.VPCAttachmentSpec
			Status          cloudv1alpha1.VPCAttachmentStatus
		}{
			attachment.Namespace, attachment.Name, attachment.UID, attachment.Generation,
			attachment.Labels, attachment.DeletionTimestamp != nil, attachment.Spec, attachment.Status,
		})
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return plan, err
	}
	sum := sha256.Sum256(encoded)
	plan.InputDigest = hex.EncodeToString(sum[:])
	for node := range nodes {
		plan.Nodes = append(plan.Nodes, node)
	}
	sort.Strings(plan.Nodes)
	return plan, nil
}

func nonNilAttachments(attachments []*cloudv1alpha1.VPCAttachment) []*cloudv1alpha1.VPCAttachment {
	result := make([]*cloudv1alpha1.VPCAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment != nil {
			result = append(result, attachment)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Namespace+"/"+result[i].Name < result[j].Namespace+"/"+result[j].Name
	})
	return result
}

func readyAttachments(attachments []*cloudv1alpha1.VPCAttachment) []*cloudv1alpha1.VPCAttachment {
	result := make([]*cloudv1alpha1.VPCAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment == nil || attachment.DeletionTimestamp != nil ||
			attachment.Status.ObservedGeneration != attachment.Generation ||
			attachment.Status.Node == "" || attachment.Status.VPC == "" || attachment.Status.VPCAttachment == "" ||
			!apimeta.IsStatusConditionTrue(attachment.Status.Conditions, cloudv1alpha1.ConditionTypeReady) ||
			!apimeta.IsStatusConditionTrue(attachment.Status.Conditions, cloudv1alpha1.ConditionTypeProgrammed) {
			continue
		}
		result = append(result, attachment)
	}
	sort.Slice(result, func(i, j int) bool {
		return types.NamespacedName{Namespace: result[i].Namespace, Name: result[i].Name}.String() <
			types.NamespacedName{Namespace: result[j].Namespace, Name: result[j].Name}.String()
	})
	return result
}

func producerCandidates(
	endpoint *networkv1alpha1.ServiceEndpoint,
	attachments []*cloudv1alpha1.VPCAttachment,
) ([]*cloudv1alpha1.VPCAttachment, error) {
	if (endpoint.Spec.AttachmentRef == nil) == (endpoint.Spec.AttachmentSelector == nil) {
		return nil, errors.New("service endpoint must select one producer source")
	}
	ready := readyAttachments(attachments)
	if endpoint.Spec.AttachmentRef != nil {
		key := types.NamespacedName{
			Namespace: endpoint.Spec.AttachmentRef.Namespace,
			Name:      endpoint.Spec.AttachmentRef.Name,
		}
		for _, attachment := range ready {
			if (types.NamespacedName{Namespace: attachment.Namespace, Name: attachment.Name}) == key &&
				attachment.Status.HostInterface != "" {
				return []*cloudv1alpha1.VPCAttachment{attachment}, nil
			}
		}
		return nil, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(endpoint.Spec.AttachmentSelector)
	if err != nil {
		return nil, err
	}
	result := make([]*cloudv1alpha1.VPCAttachment, 0, len(ready))
	for _, attachment := range ready {
		if attachment.Status.HostInterface != "" && selector.Matches(labels.Set(attachment.Labels)) {
			result = append(result, attachment)
		}
	}
	return result, nil
}

func validateProducerTopology(
	endpoint *networkv1alpha1.ServiceEndpoint,
	producers []*cloudv1alpha1.VPCAttachment,
) error {
	type location struct{ node, vpc string }
	seen := map[location]bool{}
	for _, producer := range producers {
		key := location{node: producer.Status.Node, vpc: producer.Status.VPC}
		if seen[key] {
			return fmt.Errorf("service endpoint %s selects multiple producers on node %q in VPC %q", endpoint.Name, key.node, key.vpc)
		}
		seen[key] = true
	}
	return nil
}

func chooseProducer(
	mode networkv1alpha1.ServiceEndpointDeliveryMode,
	consumerNode string,
	producers []*cloudv1alpha1.VPCAttachment,
) *cloudv1alpha1.VPCAttachment {
	for _, producer := range producers {
		if producer.Status.Node == consumerNode {
			return producer
		}
	}
	if mode == networkv1alpha1.ServiceEndpointDeliveryModePreferNodeLocal && len(producers) > 0 {
		return producers[0]
	}
	return nil
}

func consumerMatchesVPC(
	policy *networkv1alpha1.ServiceRoutePolicy,
	consumer *cloudv1alpha1.VPCAttachment,
	identity string,
) bool {
	if ref := policy.Spec.ConsumerVPCRef; ref != nil &&
		(consumer.Namespace != policy.Namespace || consumer.Spec.VPC.Name != ref.Name) {
		return false
	}
	return identity == "" || consumer.Status.VPC == identity
}
