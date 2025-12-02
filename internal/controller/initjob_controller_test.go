/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	batchinitv1alpha1 "github.com/S-mishina/initjob-operator/api/v1alpha1"
)

var _ = Describe("InitJob Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-initjob"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind InitJob")
			initjob := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, initjob)
			if err != nil && errors.IsNotFound(err) {
				resource := &batchinitv1alpha1.InitJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: batchinitv1alpha1.InitJobSpec{
						JobTemplate: batchv1.JobTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									"app": "test-init",
								},
							},
							Spec: batchv1.JobSpec{
								BackoffLimit: ptr(int32(3)),
								Template: corev1.PodTemplateSpec{
									Spec: corev1.PodSpec{
										RestartPolicy: corev1.RestartPolicyNever,
										Containers: []corev1.Container{
											{
												Name:    "init",
												Image:   "busybox",
												Command: []string{"sh", "-c", "echo init && sleep 1"},
											},
										},
									},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				By("Cleanup the specific resource instance InitJob")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource and create a Job", func() {
			By("Reconciling the created resource")
			controllerReconciler := &InitJobReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))

			By("Checking that the InitJob status was updated")
			initjob := &batchinitv1alpha1.InitJob{}
			err = k8sClient.Get(ctx, typeNamespacedName, initjob)
			Expect(err).NotTo(HaveOccurred())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhasePending))
			Expect(initjob.Status.JobName).NotTo(BeEmpty())
			Expect(initjob.Status.LastAppliedJobTemplateHash).NotTo(BeEmpty())

			By("Checking that a Job was created")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{
				Name:      initjob.Status.JobName,
				Namespace: "default",
			}
			err = k8sClient.Get(ctx, jobKey, job)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Labels["initjob.sre.example.com/name"]).To(Equal(resourceName))
		})

		It("should not create a new Job when spec has not changed", func() {
			By("First reconciliation to create the Job")
			controllerReconciler := &InitJobReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Getting the created Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			err = k8sClient.Get(ctx, typeNamespacedName, initjob)
			Expect(err).NotTo(HaveOccurred())
			firstJobName := initjob.Status.JobName
			firstHash := initjob.Status.LastAppliedJobTemplateHash

			By("Second reconciliation without spec changes")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that no new Job was created")
			err = k8sClient.Get(ctx, typeNamespacedName, initjob)
			Expect(err).NotTo(HaveOccurred())
			Expect(initjob.Status.JobName).To(Equal(firstJobName))
			Expect(initjob.Status.LastAppliedJobTemplateHash).To(Equal(firstHash))
		})
	})
})

func ptr[T any](v T) *T {
	return &v
}
