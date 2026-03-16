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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

			// Clean up any Jobs created during the test
			jobList := &batchv1.JobList{}
			err = k8sClient.List(ctx, jobList, client.InNamespace("default"))
			if err == nil {
				for i := range jobList.Items {
					propagation := metav1.DeletePropagationBackground
					_ = k8sClient.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})
				}
			}
		})

		It("should successfully reconcile the resource and create a Job", func() {
			By("Reconciling the created resource")
			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
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
			Expect(job.Labels["initjob.sre.ryu-tech.blog/name"]).To(Equal(resourceName))
		})

		It("should not create a new Job when spec has not changed", func() {
			By("First reconciliation to create the Job")
			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
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

	Context("When InitJob is not found", func() {
		It("should return no error for a deleted InitJob", func() {
			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			result, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent-initjob",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})

	Context("When Job status changes", func() {
		const resourceName = "test-status-initjob"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		var controllerReconciler *InitJobReconciler

		BeforeEach(func() {
			controllerReconciler = &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(3)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"sh", "-c", "echo hello"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			// First reconcile to create the Job
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			resource := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			jobList := &batchv1.JobList{}
			err = k8sClient.List(ctx, jobList, client.InNamespace("default"))
			if err == nil {
				for i := range jobList.Items {
					propagation := metav1.DeletePropagationBackground
					_ = k8sClient.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})
				}
			}
		})

		It("should update phase to Succeeded when Job succeeds", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			jobName := initjob.Status.JobName

			By("Simulating Job success by updating Job status")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: jobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Succeeded = 1
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobSuccessCriteriaMet, Status: "True"},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: "True"},
			)
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Reconciling again to pick up Job status")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

			By("Verifying InitJob status")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhaseSucceeded))
			Expect(initjob.Status.LastSucceeded).To(BeTrue())
			Expect(initjob.Status.LastCompletionTime).NotTo(BeNil())

			By("Verifying Ready condition is True")
			readyCond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("JobCompleted"))
		})

		It("should update phase to Failed when Job fails", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			jobName := initjob.Status.JobName

			By("Simulating Job failure by updating Job status")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: jobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 3
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: "True"},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: "True"},
			)
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Reconciling again to pick up Job status")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

			By("Verifying InitJob status")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhaseFailed))
			Expect(initjob.Status.LastSucceeded).To(BeFalse())

			By("Verifying Ready condition is False with JobFailed reason")
			readyCond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("JobFailed"))
		})

		It("should update phase to Running when Job is active", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			jobName := initjob.Status.JobName

			By("Simulating Job running by updating Job status")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: jobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			job.Status.Active = 1
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Reconciling again to pick up Job status")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			By("Verifying InitJob status is Running")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhaseRunning))

			By("Verifying Ready condition is False with JobRunning reason")
			readyCond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("JobRunning"))
		})
	})

	Context("When spec changes with diff detection", func() {
		const resourceName = "test-diff-initjob"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		var controllerReconciler *InitJobReconciler

		BeforeEach(func() {
			controllerReconciler = &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(3)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"sh", "-c", "echo v1"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			// First reconcile to create the Job
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			resource := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			jobList := &batchv1.JobList{}
			err = k8sClient.List(ctx, jobList, client.InNamespace("default"))
			if err == nil {
				for i := range jobList.Items {
					propagation := metav1.DeletePropagationBackground
					_ = k8sClient.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})
				}
			}
		})

		It("should set SpecChangedWhileRunning condition when spec changes while Job is active", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			jobName := initjob.Status.JobName

			By("Simulating Job running")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: jobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			job.Status.Active = 1
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Changing the InitJob spec")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo v2"}
			Expect(k8sClient.Update(ctx, initjob)).To(Succeed())

			By("Reconciling to detect the diff while Job is running")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			By("Verifying SpecChangedWhileRunning condition is set")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			cond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeSpecChangedWhileRunning)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("SpecChangedWhileRunning"))
		})

		It("should create a new Job when spec changes after Job succeeded", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			firstJobName := initjob.Status.JobName

			By("Simulating Job success")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: firstJobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Succeeded = 1
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobSuccessCriteriaMet, Status: "True"},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: "True"},
			)
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			// Reconcile to update status to Succeeded
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Changing the InitJob spec")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo v2"}
			Expect(k8sClient.Update(ctx, initjob)).To(Succeed())

			By("Reconciling to detect the diff")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))

			By("Verifying a new Job was created with a different name")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.JobName).NotTo(Equal(firstJobName))
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhasePending))
		})

		It("should create a new Job when spec changes after Job failed", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			firstJobName := initjob.Status.JobName

			By("Simulating Job failure")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: firstJobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 3
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: "True"},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: "True"},
			)
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			// Reconcile to update status to Failed
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Changing the InitJob spec")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo v2-fixed"}
			Expect(k8sClient.Update(ctx, initjob)).To(Succeed())

			By("Reconciling to detect the diff")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))

			By("Verifying a new Job was created")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.JobName).NotTo(Equal(firstJobName))
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhasePending))
			Expect(initjob.Status.LastSucceeded).To(BeFalse())
		})

		It("should clear SpecChangedWhileRunning condition when new Job is created after completion", func() {
			By("Getting the InitJob to find the Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			jobName := initjob.Status.JobName

			By("Simulating Job running")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: jobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			job.Status.Active = 1
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Changing the InitJob spec while Job is running")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo v2"}
			Expect(k8sClient.Update(ctx, initjob)).To(Succeed())

			// Reconcile to set SpecChangedWhileRunning
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying SpecChangedWhileRunning is set")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			cond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeSpecChangedWhileRunning)
			Expect(cond).NotTo(BeNil())

			By("Simulating Job completion (success)")
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			job.Status.Active = 0
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Succeeded = 1
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobSuccessCriteriaMet, Status: "True"},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: "True"},
			)
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Reconciling again - should create new Job and clear condition")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))

			By("Verifying SpecChangedWhileRunning condition is cleared")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			cond = meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeSpecChangedWhileRunning)
			Expect(cond).To(BeNil())

			By("Verifying new Job was created")
			Expect(initjob.Status.JobName).NotTo(Equal(jobName))
		})
	})

	Context("When verifying Job properties", func() {
		const resourceName = "test-props-initjob"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		AfterEach(func() {
			resource := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			jobList := &batchv1.JobList{}
			err = k8sClient.List(ctx, jobList, client.InNamespace("default"))
			if err == nil {
				for i := range jobList.Items {
					propagation := metav1.DeletePropagationBackground
					_ = k8sClient.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})
				}
			}
		})

		It("should set operator-managed labels on the created Job", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying operator-managed labels on the created Job")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())

			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: initjob.Status.JobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			Expect(job.Labels["app.kubernetes.io/managed-by"]).To(Equal("initjob-operator"))
			Expect(job.Labels["initjob.sre.ryu-tech.blog/name"]).To(Equal(resourceName))
		})

		It("should copy labels from jobTemplate to the created Job", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"custom-label": "custom-value",
								"team":         "sre",
							},
						},
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Verifying that jobTemplate.metadata.labels are preserved after storage")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Spec.JobTemplate.Labels["custom-label"]).To(Equal("custom-value"))

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying labels are copied to the created Job")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: initjob.Status.JobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			Expect(job.Labels["custom-label"]).To(Equal("custom-value"))
			Expect(job.Labels["team"]).To(Equal("sre"))
			Expect(job.Labels["app.kubernetes.io/managed-by"]).To(Equal("initjob-operator"))
			Expect(job.Labels["initjob.sre.ryu-tech.blog/name"]).To(Equal(resourceName))
		})

		It("should copy annotations from jobTemplate to the created Job", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"description": "migration job",
							},
						},
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying annotations are copied to the created Job")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: initjob.Status.JobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			Expect(job.Annotations["description"]).To(Equal("migration job"))
		})

		It("should set OwnerReference on the created Job", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying OwnerReference on the created Job")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())

			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: initjob.Status.JobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Name).To(Equal(resourceName))
			Expect(job.OwnerReferences[0].Kind).To(Equal("InitJob"))
			Expect(*job.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("should set JobCreated and Ready conditions on first reconciliation", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying conditions")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())

			jobCreatedCond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeJobCreated)
			Expect(jobCreatedCond).NotTo(BeNil())
			Expect(jobCreatedCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(jobCreatedCond.Reason).To(Equal("JobCreated"))

			readyCond := meta.FindStatusCondition(initjob.Status.Conditions, batchinitv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("JobStarting"))
		})
	})

	Context("When the Job is deleted externally but InitJob still exists", func() {
		const resourceName = "test-jobdeleted-initjob"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		AfterEach(func() {
			resource := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			jobList := &batchv1.JobList{}
			err = k8sClient.List(ctx, jobList, client.InNamespace("default"))
			if err == nil {
				for i := range jobList.Items {
					propagation := metav1.DeletePropagationBackground
					_ = k8sClient.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})
				}
			}
		})

		It("should handle gracefully when Job is deleted and no spec diff", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			// First reconcile to create the Job
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Getting the created Job name")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			jobName := initjob.Status.JobName

			By("Deleting the Job externally")
			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: jobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			propagation := metav1.DeletePropagationBackground
			Expect(k8sClient.Delete(ctx, job, &client.DeleteOptions{
				PropagationPolicy: &propagation,
			})).To(Succeed())

			By("Reconciling again - no diff, Job not found")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should create a new Job when spec changes and old Job was deleted", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			// First reconcile
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Getting and deleting the Job")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			firstJobName := initjob.Status.JobName

			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: firstJobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())
			propagation := metav1.DeletePropagationBackground
			Expect(k8sClient.Delete(ctx, job, &client.DeleteOptions{
				PropagationPolicy: &propagation,
			})).To(Succeed())

			By("Changing the InitJob spec")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Command = []string{"echo", "updated"}
			Expect(k8sClient.Update(ctx, initjob)).To(Succeed())

			By("Reconciling - diff detected and Job not found, should create new Job")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))

			By("Verifying new Job was created")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.JobName).NotTo(Equal(firstJobName))
		})
	})

	Context("When testing hash calculation", func() {
		It("should produce consistent hashes for the same spec", func() {
			reconciler := &InitJobReconciler{}

			template := &batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: ptr(int32(3)),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:    "init",
									Image:   "busybox",
									Command: []string{"echo", "hello"},
								},
							},
						},
					},
				},
			}

			hash1, err := reconciler.calculateJobTemplateHash(template)
			Expect(err).NotTo(HaveOccurred())

			hash2, err := reconciler.calculateJobTemplateHash(template)
			Expect(err).NotTo(HaveOccurred())

			Expect(hash1).To(Equal(hash2))
		})

		It("should produce different hashes for different specs", func() {
			reconciler := &InitJobReconciler{}

			template1 := &batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:    "init",
									Image:   "busybox",
									Command: []string{"echo", "v1"},
								},
							},
						},
					},
				},
			}

			template2 := &batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:    "init",
									Image:   "busybox",
									Command: []string{"echo", "v2"},
								},
							},
						},
					},
				},
			}

			hash1, err := reconciler.calculateJobTemplateHash(template1)
			Expect(err).NotTo(HaveOccurred())

			hash2, err := reconciler.calculateJobTemplateHash(template2)
			Expect(err).NotTo(HaveOccurred())

			Expect(hash1).NotTo(Equal(hash2))
		})
	})

	Context("When testing helper functions", func() {
		It("isJobActive should return true when Active > 0", func() {
			job := &batchv1.Job{Status: batchv1.JobStatus{Active: 1}}
			Expect(isJobActive(job)).To(BeTrue())
		})

		It("isJobActive should return false when Active == 0", func() {
			job := &batchv1.Job{Status: batchv1.JobStatus{Active: 0}}
			Expect(isJobActive(job)).To(BeFalse())
		})

		It("isJobSucceeded should return true when Succeeded > 0", func() {
			job := &batchv1.Job{Status: batchv1.JobStatus{Succeeded: 1}}
			Expect(isJobSucceeded(job)).To(BeTrue())
		})

		It("isJobSucceeded should return false when Succeeded == 0", func() {
			job := &batchv1.Job{Status: batchv1.JobStatus{Succeeded: 0}}
			Expect(isJobSucceeded(job)).To(BeFalse())
		})

		It("isJobFailed should return true when Failed condition is True", func() {
			job := &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: "True"},
					},
				},
			}
			Expect(isJobFailed(job)).To(BeTrue())
		})

		It("isJobFailed should return false when no Failed condition", func() {
			job := &batchv1.Job{Status: batchv1.JobStatus{}}
			Expect(isJobFailed(job)).To(BeFalse())
		})

		It("isJobFailed should return false when Failed condition is False", func() {
			job := &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: "False"},
					},
				},
			}
			Expect(isJobFailed(job)).To(BeFalse())
		})

		It("isJobFailed should handle multiple conditions correctly", func() {
			job := &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobComplete, Status: "False"},
						{Type: batchv1.JobFailed, Status: "True"},
						{Type: batchv1.JobSuspended, Status: "False"},
					},
				},
			}
			Expect(isJobFailed(job)).To(BeTrue())
		})
	})

	Context("Panic safety and edge cases", func() {
		const resourceName = "test-panic-initjob"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		AfterEach(func() {
			resource := &batchinitv1alpha1.InitJob{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			jobList := &batchv1.JobList{}
			err = k8sClient.List(ctx, jobList, client.InNamespace("default"))
			if err == nil {
				for i := range jobList.Items {
					propagation := metav1.DeletePropagationBackground
					_ = k8sClient.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})
				}
			}
		})

		It("should not panic when Job has zero-value status (no active, no succeeded, no failed)", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			// First reconcile creates the Job
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Job exists but has zero-value status (not active, not succeeded, not failed)
			// This is the state right after Job creation before any Pod starts
			By("Reconciling with zero-value Job status should not panic")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// Should stay Pending and requeue
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhasePending))
		})

		It("should not panic when Job succeeds without CompletionTime", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Setting Job as succeeded without CompletionTime")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())

			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: initjob.Status.JobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			// Succeeded > 0 but no CompletionTime (edge case)
			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Reconciling should not panic on nil CompletionTime")
			Expect(func() {
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
			}).NotTo(Panic())

			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhaseSucceeded))
			// CompletionTime should be nil since Job didn't have one
			Expect(initjob.Status.LastCompletionTime).To(BeNil())
		})

		It("should not panic when Job fails without CompletionTime", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Setting Job as failed without CompletionTime")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())

			job := &batchv1.Job{}
			jobKey := types.NamespacedName{Name: initjob.Status.JobName, Namespace: "default"}
			Expect(k8sClient.Get(ctx, jobKey, job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 3
			// Failed condition without CompletionTime
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: "True"},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: "True"},
			)
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			By("Reconciling should not panic on nil CompletionTime for failed Job")
			Expect(func() {
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
			}).NotTo(Panic())

			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.Phase).To(Equal(batchinitv1alpha1.InitJobPhaseFailed))
		})

		It("should not panic with rapid consecutive reconciles", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			By("Running multiple reconciles in rapid succession should not panic")
			Expect(func() {
				for i := 0; i < 10; i++ {
					_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespacedName,
					})
				}
			}).NotTo(Panic())

			By("Verifying final state is consistent")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.JobName).NotTo(BeEmpty())
			Expect(initjob.Status.LastAppliedJobTemplateHash).NotTo(BeEmpty())
		})

		It("should not panic when reconciling with multiple containers", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "migrate",
											Image:   "postgres:15",
											Command: []string{"psql", "-c", "SELECT 1"},
										},
										{
											Name:    "verify",
											Image:   "busybox",
											Command: []string{"echo", "verified"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			Expect(func() {
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
			}).NotTo(Panic())

			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.JobName).NotTo(BeEmpty())
		})

		It("should produce a hash with at least 8 characters", func() {
			reconciler := &InitJobReconciler{}

			// Even with minimal template, hash should be >= 8 chars (SHA256 = 64 hex chars)
			template := &batchv1.JobTemplateSpec{}
			hash, err := reconciler.calculateJobTemplateHash(template)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(hash)).To(BeNumerically(">=", 8))

			// SHA256 hex is always exactly 64 characters
			Expect(hash).To(HaveLen(64))
		})

		It("should not panic and should recover when InitJob status is manually cleared", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "test"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			// First reconcile
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Manually clearing the InitJob status (simulating external edit)")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Status = batchinitv1alpha1.InitJobStatus{}
			Expect(k8sClient.Status().Update(ctx, initjob)).To(Succeed())

			By("Reconciling after status reset should not panic and should recover status")
			Expect(func() {
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
			}).NotTo(Panic())

			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			Expect(initjob.Status.JobName).NotTo(BeEmpty())
			Expect(initjob.Status.LastAppliedJobTemplateHash).NotTo(BeEmpty())
		})

		It("should not panic when Job name in status references a non-existent Job and spec changes", func() {
			resource := &batchinitv1alpha1.InitJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: batchinitv1alpha1.InitJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							BackoffLimit: ptr(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "init",
											Image:   "busybox",
											Command: []string{"echo", "v1"},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &InitJobReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			// First reconcile
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Manually setting a bogus Job name in status")
			initjob := &batchinitv1alpha1.InitJob{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Status.JobName = "non-existent-job-12345678"
			Expect(k8sClient.Status().Update(ctx, initjob)).To(Succeed())

			By("Changing spec to trigger diff")
			Expect(k8sClient.Get(ctx, typeNamespacedName, initjob)).To(Succeed())
			initjob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Command = []string{"echo", "v2"}
			Expect(k8sClient.Update(ctx, initjob)).To(Succeed())

			By("Reconciling with bogus Job name + diff should not panic")
			Expect(func() {
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
			}).NotTo(Panic())
		})
	})
})

func ptr[T any](v T) *T {
	return &v
}
