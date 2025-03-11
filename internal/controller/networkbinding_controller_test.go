// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// TODO(jreese) replace with chainsaw

var _ = Describe("NetworkBinding Controller", func() {
	Context("When reconciling a new resource", Ordered, func() {
		const networkName = "test-binding-network"
		const bindingName = "test-binding"

		ctx := context.Background()

		networkNamespacedName := types.NamespacedName{
			Name:      networkName,
			Namespace: "default",
		}
		network := &networkingv1alpha.Network{}

		bindingNamespacedName := types.NamespacedName{
			Name:      bindingName,
			Namespace: "default",
		}
		binding := &networkingv1alpha.NetworkBinding{}

		BeforeEach(func() {
			By("creating a Network")
			err := k8sClient.Get(ctx, networkNamespacedName, network)
			if err != nil && errors.IsNotFound(err) {
				network = &networkingv1alpha.Network{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      networkName,
					},
					Spec: networkingv1alpha.NetworkSpec{
						IPAM: networkingv1alpha.NetworkIPAM{
							Mode: networkingv1alpha.NetworkIPAMModeAuto,
						},
					},
				}
				Expect(k8sClient.Create(ctx, network)).To(Succeed())
			}
			Expect(client.IgnoreNotFound(err)).To(Succeed())

			By("creating a NetworkBinding")
			err = k8sClient.Get(ctx, bindingNamespacedName, binding)
			if err != nil && errors.IsNotFound(err) {
				resource := &networkingv1alpha.NetworkBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      bindingName,
						Namespace: "default",
					},
					Spec: networkingv1alpha.NetworkBindingSpec{
						Network: networkingv1alpha.NetworkRef{
							Name: network.Name,
						},
						Location: networkingv1alpha.LocationReference{
							Namespace: "default",
							Name:      "some-location",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
			Expect(client.IgnoreNotFound(err)).To(Succeed())

		})

		AfterEach(func() {
			binding := &networkingv1alpha.NetworkBinding{}
			Expect(k8sClient.Get(ctx, bindingNamespacedName, binding)).To(Succeed())

			networkContextName := networkContextNameForBinding(binding)
			Expect(k8sClient.Delete(ctx, binding)).To(Succeed())

			networkContext := &networkingv1alpha.NetworkContext{}
			networkContextNamespacedName := types.NamespacedName{
				Name:      networkContextName,
				Namespace: "default",
			}
			Expect(k8sClient.Get(ctx, networkContextNamespacedName, networkContext)).To(Succeed())
			Expect(k8sClient.Delete(ctx, networkContext)).To(Succeed())

			network := &networkingv1alpha.Network{}
			Expect(k8sClient.Get(ctx, networkNamespacedName, network)).To(Succeed())
			Expect(k8sClient.Delete(ctx, network)).To(Succeed())
		})

		It("should successfully create a NetworkContext", func() {
			err := k8sClient.Get(ctx, bindingNamespacedName, binding)
			Expect(err).ToNot(HaveOccurred())

			bindingReady := apimeta.IsStatusConditionTrue(binding.Status.Conditions, networkingv1alpha.NetworkBindingReady)
			Expect(bindingReady).To(BeFalse())

			networkContextName := networkContextNameForBinding(binding)

			var networkContext networkingv1alpha.NetworkContext
			networkContextObjectKey := client.ObjectKey{
				Namespace: binding.Namespace,
				Name:      networkContextName,
			}

			Eventually(ctx, func() error {
				return k8sClient.Get(ctx, networkContextObjectKey, &networkContext)
			}).Should(Succeed())
		})

		It("should become Ready once the referenced NetworkContext is Ready", func() {
			networkContextName := networkContextNameForBinding(binding)

			var networkContext networkingv1alpha.NetworkContext
			networkContextObjectKey := client.ObjectKey{
				Namespace: binding.Namespace,
				Name:      networkContextName,
			}

			Eventually(ctx, func() error {
				return k8sClient.Get(ctx, networkContextObjectKey, &networkContext)
			}).Should(Succeed())

			// We set the status manually here, as external controllers are responsible
			// for updating Context readiness right now.
			//
			// TODO(jreese) - Consider having a `Programmed` condition that external
			// controllers use, and have a NSO controller update the `Ready` condition?

			apimeta.SetStatusCondition(&networkContext.Status.Conditions, metav1.Condition{
				Type:    networkingv1alpha.NetworkContextReady,
				Status:  metav1.ConditionTrue,
				Reason:  "Test",
				Message: "test condition",
			})

			Expect(k8sClient.Status().Update(ctx, &networkContext)).To(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, bindingNamespacedName, binding)
				Expect(err).ToNot(HaveOccurred())

				return apimeta.IsStatusConditionTrue(binding.Status.Conditions, networkingv1alpha.NetworkBindingReady)
			}).Should(BeTrue())
		})
	})
})
