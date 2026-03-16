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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchinitv1alpha1 "github.com/S-mishina/initjob-operator/api/v1alpha1"
)

// InitJobReconciler reconciles a InitJob object
type InitJobReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=batch.init.sre.ryu-tech.blog,resources=initjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch.init.sre.ryu-tech.blog,resources=initjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch.init.sre.ryu-tech.blog,resources=initjobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// InitJob Operator reconcile logic:
// 1. Fetch the InitJob
// 2. Calculate hash from current spec.jobTemplate
// 3. Compare with status.lastAppliedJobTemplateHash
// 4. Check Job existence and status
// 5. Create/skip Job based on diff detection
func (r *InitJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the InitJob
	var initJob batchinitv1alpha1.InitJob
	if err := r.Get(ctx, req.NamespacedName, &initJob); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("InitJob not found, likely deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch InitJob")
		return ctrl.Result{}, err
	}

	// Record the observed generation for status tracking
	initJob.Status.ObservedGeneration = initJob.Generation

	log.Info("Reconciling InitJob",
		"initjob", initJob.Name,
		"namespace", initJob.Namespace,
		"phase", initJob.Status.Phase,
	)

	// 2. Calculate hash from current spec.jobTemplate
	currentHash, err := r.calculateJobTemplateHash(&initJob.Spec.JobTemplate)
	if err != nil {
		log.Error(err, "failed to calculate jobTemplate hash")
		return ctrl.Result{}, err
	}

	log.Info("Hash calculated",
		"currentHash", currentHash,
		"lastAppliedHash", initJob.Status.LastAppliedJobTemplateHash,
	)

	// 3. Compare with status.lastAppliedJobTemplateHash
	hasDiff := currentHash != initJob.Status.LastAppliedJobTemplateHash
	isFirstRun := initJob.Status.LastAppliedJobTemplateHash == ""

	// 4. Check Job existence and status
	var existingJob *batchv1.Job
	if initJob.Status.JobName != "" {
		existingJob = &batchv1.Job{}
		jobKey := client.ObjectKey{
			Namespace: initJob.Namespace,
			Name:      initJob.Status.JobName,
		}
		if err := r.Get(ctx, jobKey, existingJob); err != nil {
			if apierrors.IsNotFound(err) {
				existingJob = nil
			} else {
				log.Error(err, "failed to get existing Job", "jobName", initJob.Status.JobName)
				return ctrl.Result{}, err
			}
		}
	}

	// 5. Determine action based on diff detection
	return r.reconcileJob(ctx, &initJob, existingJob, currentHash, hasDiff, isFirstRun)
}

// reconcileJob handles the core reconciliation logic for Job creation/status updates
func (r *InitJobReconciler) reconcileJob(
	ctx context.Context,
	initJob *batchinitv1alpha1.InitJob,
	existingJob *batchv1.Job,
	currentHash string,
	hasDiff bool,
	isFirstRun bool,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Case 1: First run - create Job
	if isFirstRun {
		log.Info("First run detected, creating Job")
		return r.createJob(ctx, initJob, currentHash)
	}

	// Case 2: No diff - no action needed
	if !hasDiff {
		log.Info("No diff detected, skipping Job creation")
		// Update status based on existing Job if present
		if existingJob != nil {
			return r.updateStatusFromJob(ctx, initJob, existingJob)
		}
		// No Job exists and no diff - nothing to do
		return ctrl.Result{}, nil
	}

	// Case 3: Diff detected
	log.Info("Diff detected in jobTemplate")

	// Check if Job is still running
	if existingJob != nil && isJobActive(existingJob) {
		log.Info("Job is still active, cannot update while running",
			"jobName", existingJob.Name,
		)
		// Record condition that spec changed while running
		return r.setSpecChangedWhileRunningCondition(ctx, initJob, currentHash)
	}

	// Case 4: Diff detected and Job is completed or doesn't exist - create new Job
	log.Info("Creating new Job due to diff",
		"oldHash", initJob.Status.LastAppliedJobTemplateHash,
		"newHash", currentHash,
	)
	return r.createJob(ctx, initJob, currentHash)
}

// createJob creates a new Job from the InitJob's jobTemplate
func (r *InitJobReconciler) createJob(
	ctx context.Context,
	initJob *batchinitv1alpha1.InitJob,
	hash string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Generate Job name with short hash suffix
	shortHash := hash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	jobName := fmt.Sprintf("%s-%s", initJob.Name, shortHash)

	// Build the Job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: initJob.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "initjob-operator",
				"initjob.sre.ryu-tech.blog/name": initJob.Name,
			},
		},
		Spec: initJob.Spec.JobTemplate.Spec,
	}

	// Copy labels from jobTemplate.metadata if present
	if initJob.Spec.JobTemplate.Labels != nil {
		if job.Labels == nil {
			job.Labels = make(map[string]string)
		}
		for k, v := range initJob.Spec.JobTemplate.Labels {
			job.Labels[k] = v
		}
	}

	// Copy annotations from jobTemplate.metadata if present
	if initJob.Spec.JobTemplate.Annotations != nil {
		job.Annotations = initJob.Spec.JobTemplate.Annotations
	}

	// Set owner reference for garbage collection
	if err := controllerutil.SetControllerReference(initJob, job, r.Scheme); err != nil {
		log.Error(err, "failed to set controller reference")
		return ctrl.Result{}, err
	}

	// Create the Job
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Info("Job already exists", "jobName", jobName)
			// Job already exists, restore status fields and update from Job
			var existingJob batchv1.Job
			if err := r.Get(ctx, client.ObjectKey{Namespace: initJob.Namespace, Name: jobName}, &existingJob); err != nil {
				return ctrl.Result{}, err
			}
			initJob.Status.JobName = jobName
			initJob.Status.LastAppliedJobTemplateHash = hash
			return r.updateStatusFromJob(ctx, initJob, &existingJob)
		}
		log.Error(err, "failed to create Job")
		return ctrl.Result{}, err
	}

	log.Info("Job created successfully", "jobName", jobName)
	r.Recorder.Eventf(initJob, "Normal", "JobCreated", "Created Job %s", jobName)

	// Update InitJob status
	initJob.Status.JobName = jobName
	initJob.Status.LastAppliedJobTemplateHash = hash
	initJob.Status.Phase = batchinitv1alpha1.InitJobPhasePending
	initJob.Status.LastSucceeded = false

	// Clear the SpecChangedWhileRunning condition if it was set
	meta.RemoveStatusCondition(&initJob.Status.Conditions, batchinitv1alpha1.ConditionTypeSpecChangedWhileRunning)

	// Set JobCreated condition
	meta.SetStatusCondition(&initJob.Status.Conditions, metav1.Condition{
		Type:               batchinitv1alpha1.ConditionTypeJobCreated,
		Status:             metav1.ConditionTrue,
		Reason:             "JobCreated",
		Message:            fmt.Sprintf("Job %s created", jobName),
		LastTransitionTime: metav1.Now(),
	})

	// Set Ready condition to false (job is starting)
	meta.SetStatusCondition(&initJob.Status.Conditions, metav1.Condition{
		Type:               batchinitv1alpha1.ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             "JobStarting",
		Message:            "Job is starting",
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, initJob); err != nil {
		log.Error(err, "failed to update InitJob status after Job creation")
		return ctrl.Result{}, err
	}

	// Requeue to check Job status
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// updateStatusFromJob updates the InitJob status based on the Job's status
func (r *InitJobReconciler) updateStatusFromJob(
	ctx context.Context,
	initJob *batchinitv1alpha1.InitJob,
	job *batchv1.Job,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	originalPhase := initJob.Status.Phase
	originalSucceeded := initJob.Status.LastSucceeded
	originalJobName := initJob.Status.JobName
	var requeue bool

	// Determine phase based on Job status
	switch {
	case isJobSucceeded(job):
		initJob.Status.Phase = batchinitv1alpha1.InitJobPhaseSucceeded
		initJob.Status.LastSucceeded = true
		if job.Status.CompletionTime != nil {
			initJob.Status.LastCompletionTime = job.Status.CompletionTime
		}
		meta.SetStatusCondition(&initJob.Status.Conditions, metav1.Condition{
			Type:               batchinitv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			Reason:             "JobCompleted",
			Message:            "Job succeeded",
			LastTransitionTime: metav1.Now(),
		})
		if originalPhase != batchinitv1alpha1.InitJobPhaseSucceeded {
			r.Recorder.Eventf(initJob, "Normal", "JobSucceeded", "Job %s succeeded", initJob.Status.JobName)
		}
	case isJobFailed(job):
		initJob.Status.Phase = batchinitv1alpha1.InitJobPhaseFailed
		initJob.Status.LastSucceeded = false
		if job.Status.CompletionTime != nil {
			initJob.Status.LastCompletionTime = job.Status.CompletionTime
		}
		meta.SetStatusCondition(&initJob.Status.Conditions, metav1.Condition{
			Type:               batchinitv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "JobFailed",
			Message:            "Job failed",
			LastTransitionTime: metav1.Now(),
		})
		if originalPhase != batchinitv1alpha1.InitJobPhaseFailed {
			r.Recorder.Eventf(initJob, "Warning", "JobFailed", "Job %s failed", initJob.Status.JobName)
		}
	case isJobActive(job):
		initJob.Status.Phase = batchinitv1alpha1.InitJobPhaseRunning
		meta.SetStatusCondition(&initJob.Status.Conditions, metav1.Condition{
			Type:               batchinitv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "JobRunning",
			Message:            "Job is running",
			LastTransitionTime: metav1.Now(),
		})
		requeue = true
	default:
		initJob.Status.Phase = batchinitv1alpha1.InitJobPhasePending
		requeue = true
	}

	// Update if any status field changed
	statusChanged := originalPhase != initJob.Status.Phase ||
		originalSucceeded != initJob.Status.LastSucceeded ||
		originalJobName != initJob.Status.JobName
	if statusChanged {
		log.Info("Updating InitJob status",
			"oldPhase", originalPhase,
			"newPhase", initJob.Status.Phase,
		)
		if err := r.Status().Update(ctx, initJob); err != nil {
			log.Error(err, "failed to update InitJob status")
			return ctrl.Result{}, err
		}
	}

	if requeue {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// setSpecChangedWhileRunningCondition sets a condition indicating spec changed while Job was running
func (r *InitJobReconciler) setSpecChangedWhileRunningCondition(
	ctx context.Context,
	initJob *batchinitv1alpha1.InitJob,
	newHash string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	meta.SetStatusCondition(&initJob.Status.Conditions, metav1.Condition{
		Type:   batchinitv1alpha1.ConditionTypeSpecChangedWhileRunning,
		Status: metav1.ConditionTrue,
		Reason: "SpecChangedWhileRunning",
		Message: fmt.Sprintf(
			"spec.jobTemplate was changed while Job is running. New hash: %s, Current hash: %s. Delete or wait for Job to complete.",
			truncateHash(newHash),
			truncateHash(initJob.Status.LastAppliedJobTemplateHash),
		),
		LastTransitionTime: metav1.Now(),
	})

	r.Recorder.Eventf(initJob, "Warning", "SpecChangedWhileRunning",
		"spec.jobTemplate changed while Job is running (current: %s, new: %s)",
		truncateHash(initJob.Status.LastAppliedJobTemplateHash), truncateHash(newHash))

	if err := r.Status().Update(ctx, initJob); err != nil {
		log.Error(err, "failed to update InitJob status with SpecChangedWhileRunning condition")
		return ctrl.Result{}, err
	}

	log.Info("Spec changed while Job is running, condition set",
		"currentJobHash", initJob.Status.LastAppliedJobTemplateHash,
		"pendingHash", newHash,
	)

	// Requeue to check when Job completes
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// calculateJobTemplateHash calculates a stable SHA256 hash of the JobTemplateSpec
func (r *InitJobReconciler) calculateJobTemplateHash(template *batchv1.JobTemplateSpec) (string, error) {
	// Marshal to JSON for consistent hashing
	data, err := json.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("failed to marshal jobTemplate: %w", err)
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

const hashDisplayLength = 8

// truncateHash safely truncates a hash string for display purposes
func truncateHash(hash string) string {
	if len(hash) <= hashDisplayLength {
		return hash
	}
	return hash[:hashDisplayLength]
}

// Helper functions to check Job status
func isJobActive(job *batchv1.Job) bool {
	return job.Status.Active > 0
}

func isJobSucceeded(job *batchv1.Job) bool {
	return job.Status.Succeeded > 0
}

func isJobFailed(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == "True" {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *InitJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("initjob-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&batchinitv1alpha1.InitJob{}).
		Owns(&batchv1.Job{}).
		Named("initjob").
		Complete(r)
}
