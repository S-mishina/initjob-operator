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

package v1alpha1

import (
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InitJobPhase represents the current phase of the InitJob
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
type InitJobPhase string

const (
	// InitJobPhasePending indicates that the InitJob is waiting for the Job to be created or started
	InitJobPhasePending InitJobPhase = "Pending"
	// InitJobPhaseRunning indicates that the Job is currently running
	InitJobPhaseRunning InitJobPhase = "Running"
	// InitJobPhaseSucceeded indicates that the Job completed successfully
	InitJobPhaseSucceeded InitJobPhase = "Succeeded"
	// InitJobPhaseFailed indicates that the Job failed
	InitJobPhaseFailed InitJobPhase = "Failed"
)

// Condition types for InitJob
const (
	// ConditionTypeReady indicates whether the InitJob is ready
	ConditionTypeReady = "Ready"
	// ConditionTypeJobCreated indicates whether the Job has been created
	ConditionTypeJobCreated = "JobCreated"
	// ConditionTypeSpecChangedWhileRunning indicates the spec changed while Job was running
	ConditionTypeSpecChangedWhileRunning = "SpecChangedWhileRunning"
)

// InitJobSpec defines the desired state of InitJob
type InitJobSpec struct {
	// jobTemplate specifies the Job template to execute for initialization.
	// This represents the "desired init processing" that will run once.
	// +required
	JobTemplate batchv1.JobTemplateSpec `json:"jobTemplate"`
}

// InitJobStatus defines the observed state of InitJob.
type InitJobStatus struct {
	// phase represents the current phase of the InitJob (Pending/Running/Succeeded/Failed)
	// +optional
	Phase InitJobPhase `json:"phase,omitempty"`

	// jobName is the name of the most recently associated Job
	// +optional
	JobName string `json:"jobName,omitempty"`

	// lastCompletionTime is the time when the Job last completed
	// +optional
	LastCompletionTime *metav1.Time `json:"lastCompletionTime,omitempty"`

	// lastSucceeded indicates whether the last Job execution succeeded
	// +optional
	LastSucceeded bool `json:"lastSucceeded,omitempty"`

	// lastAppliedJobTemplateHash is the hash of the jobTemplate used for the last execution.
	// This is used for diff detection to determine if re-execution is needed.
	// +optional
	LastAppliedJobTemplateHash string `json:"lastAppliedJobTemplateHash,omitempty"`

	// conditions represent the current state of the InitJob resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Current phase of the InitJob"
// +kubebuilder:printcolumn:name="Job",type="string",JSONPath=".status.jobName",description="Name of the associated Job"
// +kubebuilder:printcolumn:name="Succeeded",type="boolean",JSONPath=".status.lastSucceeded",description="Whether last execution succeeded"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// InitJob is the Schema for the initjobs API.
// InitJob manages declarative initialization processing on Kubernetes.
// When an InitJob CR is created, a Kubernetes Job is executed once based on its spec.jobTemplate.
// Even if the Job is deleted, the InitJob CR remains as a record of the execution result.
// A Job is only re-executed when a change to the InitJob is detected as a "diff" from the previous execution.
type InitJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of InitJob
	// +required
	Spec InitJobSpec `json:"spec"`

	// status defines the observed state of InitJob
	// +optional
	Status InitJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InitJobList contains a list of InitJob
type InitJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InitJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InitJob{}, &InitJobList{})
}
