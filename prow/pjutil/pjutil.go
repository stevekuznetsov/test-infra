/*
Copyright 2017 The Kubernetes Authors.

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

// Package pjutil contains helpers for working with ProwJobs.
package pjutil

import (
	"fmt"
	"path"
	"time"

	"github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"

	"encoding/json"

	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
)

// NewProwJob initializes a ProwJob out of a ProwJobSpec.
func NewProwJob(spec kube.ProwJobSpec, labels map[string]string) kube.ProwJob {
	return kube.ProwJob{
		APIVersion: "prow.k8s.io/v1",
		Kind:       "ProwJob",
		Metadata: kube.ObjectMeta{
			Name:   uuid.NewV1().String(),
			Labels: labels,
		},
		Spec: spec,
		Status: kube.ProwJobStatus{
			StartTime: time.Now(),
			State:     kube.TriggeredState,
		},
	}
}

// PresubmitSpec initializes a ProwJobSpec for a given presubmit job.
func PresubmitSpec(p config.Presubmit, refs kube.Refs) kube.ProwJobSpec {
	pjs := kube.ProwJobSpec{
		Type: kube.PresubmitJob,
		Job:  p.Name,
		Refs: refs,

		Report:         !p.SkipReport,
		Context:        p.Context,
		RerunCommand:   p.RerunCommand,
		MaxConcurrency: p.MaxConcurrency,
	}
	pjs.Agent = kube.ProwJobAgent(p.Agent)
	if pjs.Agent == kube.KubernetesAgent {
		pjs.PodSpec = *p.Spec
	}
	for _, nextP := range p.RunAfterSuccess {
		pjs.RunAfterSuccess = append(pjs.RunAfterSuccess, PresubmitSpec(nextP, refs))
	}
	return pjs
}

// PostsubmitSpec initializes a ProwJobSpec for a given postsubmit job.
func PostsubmitSpec(p config.Postsubmit, refs kube.Refs) kube.ProwJobSpec {
	pjs := kube.ProwJobSpec{
		Type:           kube.PostsubmitJob,
		Job:            p.Name,
		Refs:           refs,
		MaxConcurrency: p.MaxConcurrency,
	}
	pjs.Agent = kube.ProwJobAgent(p.Agent)
	if pjs.Agent == kube.KubernetesAgent {
		pjs.PodSpec = *p.Spec
	}
	for _, nextP := range p.RunAfterSuccess {
		pjs.RunAfterSuccess = append(pjs.RunAfterSuccess, PostsubmitSpec(nextP, refs))
	}
	return pjs
}

// PeriodicSpec initializes a ProwJobSpec for a given periodic job.
func PeriodicSpec(p config.Periodic) kube.ProwJobSpec {
	pjs := kube.ProwJobSpec{
		Type: kube.PeriodicJob,
		Job:  p.Name,
	}
	pjs.Agent = kube.ProwJobAgent(p.Agent)
	if pjs.Agent == kube.KubernetesAgent {
		pjs.PodSpec = *p.Spec
	}
	for _, nextP := range p.RunAfterSuccess {
		pjs.RunAfterSuccess = append(pjs.RunAfterSuccess, PeriodicSpec(nextP))
	}
	return pjs
}

// BatchSpec initializes a ProwJobSpec for a given batch job and ref spec.
func BatchSpec(p config.Presubmit, refs kube.Refs) kube.ProwJobSpec {
	pjs := kube.ProwJobSpec{
		Type:    kube.BatchJob,
		Job:     p.Name,
		Refs:    refs,
		Context: p.Context, // The Submit Queue's getCompleteBatches needs this.
	}
	pjs.Agent = kube.ProwJobAgent(p.Agent)
	if pjs.Agent == kube.KubernetesAgent {
		pjs.PodSpec = *p.Spec
	}
	for _, nextP := range p.RunAfterSuccess {
		pjs.RunAfterSuccess = append(pjs.RunAfterSuccess, BatchSpec(nextP, refs))
	}
	return pjs
}

const (
	toolsVolumeName = "tools"
	toolsMountPath  = "/var/run/tools"

	loggingVolumeName = "logging"
	loggingMountPath  = "/var/run/logging"

	gcsCredentialsVolumeName = "gcs-credentials"
	gcsCredentialsMountPath  = "/var/run/secrets/gce"

	artifactsVolumeName = "artifacts"
	artifactsMountPath  = "/var/run/artifacts"
	artifactsEnvVarName = "GCS_ARTIFACTS_DIR"

	sourceVolumeName = "source"
	soureceMountPath = "/go"
)

var (
	processLog     = path.Join(loggingMountPath, "process.log")
	markerFile     = path.Join(loggingMountPath, "process.marker")
	gcsCredentials = path.Join(gcsCredentialsMountPath, "gcs.json")
	cloneLog       = path.Join(artifactsMountPath, "clone.json")
)

// ProwJobToDecoratedPod converts a ProwJob to a Pod that will run the tests
// and decorates it with pod utilities.
func ProwJobToDecoratedPod(pj kube.ProwJob, buildID, cloneRefsImage, initUploadImage, entrypointImage, sidecarImage string, gcsConfig *config.GoogleCloudStorage) (*kube.Pod, error) {
	prowJob, err := deepCopy(pj)
	if err != nil {
		return nil, err
	}

	// toolsVolumeMount contains the entrypoint tooling
	toolsVolumeMount := kube.VolumeMount{
		Name:      toolsVolumeName,
		MountPath: toolsMountPath,
	}

	// loggingVolumeMount contains logging files
	loggingVolumeMount := kube.VolumeMount{
		Name:      loggingVolumeName,
		MountPath: loggingMountPath,
	}

	// artifactsVolumeMount shares the artifacts between the test and upload containers
	artifactsVolumeMount := kube.VolumeMount{
		Name:      artifactsVolumeName,
		MountPath: artifactsMountPath,
	}

	// sourceVolumeMount shares the cloned source between the clone-refs and test containers
	sourceVolumeMount := kube.VolumeMount{
		Name:      sourceVolumeName,
		MountPath: soureceMountPath,
	}

	// gcsCredentialsVolumeMount holds GCS credentials for containers that upload data
	gcsCredentialsVolumeMount := kube.VolumeMount{
		Name:      gcsCredentialsVolumeName,
		ReadOnly:  true,
		MountPath: gcsCredentialsMountPath,
	}

	prowJob.Spec.PodSpec.InitContainers = []kube.Container{
		{ // the `clone-refs` init-container clones the necessary repos
			Name:    "clone-refs",
			Image:   cloneRefsImage,
			Command: []string{"/clonerefs"},
			Args: []string{
				fmt.Sprintf("-src-root=%s", soureceMountPath),
				fmt.Sprintf("-clone-log=%s", cloneLog),
			},
			VolumeMounts: []kube.VolumeMount{artifactsVolumeMount, sourceVolumeMount},
		},
		{ // the `init-upload` init-container creates started.json and uploads to GCS
			Name:    "init-upload",
			Image:   initUploadImage,
			Command: []string{"/initupload"},
			Args: append(flagsForGcsConfig(gcsConfig),
				fmt.Sprintf("-clone-log=%s", cloneLog),
			),
			VolumeMounts: []kube.VolumeMount{artifactsVolumeMount, gcsCredentialsVolumeMount},
		},
		{ // the `place-tooling` init-container adds the entrypoint to the shared volume
			Name:         "place-tooling",
			Image:        entrypointImage,
			Command:      []string{"/usr/bin/cp"},
			Args:         []string{"/entrypoint", toolsMountPath},
			VolumeMounts: []kube.VolumeMount{toolsVolumeMount},
		},
	}

	// amend the test container to add our volumes, wrap the entrypoint and communicate artifact dir
	testEntrypoint := append(pj.Spec.PodSpec.Containers[0].Command, pj.Spec.PodSpec.Containers[0].Args...)
	prowJob.Spec.PodSpec.Containers[0].Name = "test"
	prowJob.Spec.PodSpec.Containers[0].VolumeMounts = append(prowJob.Spec.PodSpec.Containers[0].VolumeMounts, toolsVolumeMount, loggingVolumeMount, artifactsVolumeMount, sourceVolumeMount)
	prowJob.Spec.PodSpec.Containers[0].Command = []string{path.Join(toolsMountPath, "entrypoint")}
	prowJob.Spec.PodSpec.Containers[0].Args = append([]string{
		fmt.Sprintf("-process-log=%s", processLog),
		fmt.Sprintf("-marker-file=%s", markerFile),
		"--",
	}, testEntrypoint...)
	prowJob.Spec.PodSpec.Containers[0].Env = append(prowJob.Spec.PodSpec.Containers[0].Env, kube.EnvVar{Name: artifactsEnvVarName, Value: artifactsMountPath})

	// add the logging sidecar container
	prowJob.Spec.PodSpec.Containers = append(prowJob.Spec.PodSpec.Containers, kube.Container{
		Name:    "sidecar",
		Image:   sidecarImage,
		Command: []string{"/sidecar"},
		Args: append(flagsForGcsConfig(gcsConfig),
			fmt.Sprintf("-process-log=%s", processLog),
			fmt.Sprintf("-marker-file=%s", markerFile),
			fmt.Sprintf("-artifact-dir=%s", artifactsMountPath),
			fmt.Sprintf("-gcs-credentials-file=%s", gcsCredentials),
		),
		VolumeMounts: []kube.VolumeMount{loggingVolumeMount, artifactsVolumeMount, gcsCredentialsVolumeMount},
	})

	// add the volumes we need to the pod
	prowJob.Spec.PodSpec.Volumes = append(prowJob.Spec.PodSpec.Volumes,
		kube.Volume{
			Name: toolsVolumeName,
			VolumeSource: kube.VolumeSource{
				EmptyDir: &kube.EmptyDirVolumeSource{},
			},
		},
		kube.Volume{
			Name: loggingVolumeName,
			VolumeSource: kube.VolumeSource{
				EmptyDir: &kube.EmptyDirVolumeSource{},
			},
		},
		kube.Volume{
			Name: artifactsVolumeName,
			VolumeSource: kube.VolumeSource{
				EmptyDir: &kube.EmptyDirVolumeSource{},
			},
		},
		kube.Volume{
			Name: sourceVolumeName,
			VolumeSource: kube.VolumeSource{
				EmptyDir: &kube.EmptyDirVolumeSource{},
			},
		},
		kube.Volume{
			Name: gcsCredentialsVolumeName,
			VolumeSource: kube.VolumeSource{
				Secret: &kube.SecretSource{
					Name: gcsConfig.CredentialsSecretName,
				},
			},
		},
	)

	return ProwJobToPod(prowJob, buildID)
}

func flagsForGcsConfig(gcsConfig *config.GoogleCloudStorage) []string {
	if gcsConfig == nil || gcsConfig.Bucket == "" {
		return []string{"-dry-run=true"}
	}

	flags := []string{
		fmt.Sprintf("-gcs-bucket=%s", gcsConfig.Bucket),
		"-dry-run=false",
	}
	if gcsConfig.PathStrategy != "" {
		flags = append(flags, fmt.Sprintf("-path-strategy=%s", gcsConfig.PathStrategy))
	}
	if gcsConfig.DefaultOrg != "" {
		flags = append(flags, fmt.Sprintf("-default-org=%s", gcsConfig.DefaultOrg))
	}
	if gcsConfig.DefaultRepo != "" {
		flags = append(flags, fmt.Sprintf("-default-repo=%s", gcsConfig.DefaultRepo))
	}

	return flags
}

// ProwJobToPod converts a ProwJob to a Pod that will run the tests.
func ProwJobToPod(pj kube.ProwJob, buildID string) (*kube.Pod, error) {
	env, err := EnvForSpec(NewJobSpec(pj.Spec, buildID))
	if err != nil {
		return nil, err
	}

	prowJob, err := deepCopy(pj)
	if err != nil {
		return nil, err
	}

	prowJob.Spec.PodSpec.RestartPolicy = "Never"
	for i := range prowJob.Spec.PodSpec.InitContainers {
		prowJob.Spec.PodSpec.InitContainers[i].Env = append(prowJob.Spec.PodSpec.InitContainers[i].Env, kubeEnv(env)...)
	}
	for i := range prowJob.Spec.PodSpec.Containers {
		prowJob.Spec.PodSpec.Containers[i].Env = append(prowJob.Spec.PodSpec.Containers[i].Env, kubeEnv(env)...)
	}
	if prowJob.Metadata.Labels == nil {
		prowJob.Metadata.Labels = make(map[string]string)
	}
	prowJob.Metadata.Labels[kube.CreatedByProw] = "true"
	prowJob.Metadata.Labels[kube.ProwJobTypeLabel] = string(prowJob.Spec.Type)
	return &kube.Pod{
		Metadata: kube.ObjectMeta{
			Name:   prowJob.Metadata.Name,
			Labels: prowJob.Metadata.Labels,
			Annotations: map[string]string{
				kube.ProwJobAnnotation: prowJob.Spec.Job,
			},
		},
		Spec: prowJob.Spec.PodSpec,
	}, nil
}

func deepCopy(pj kube.ProwJob) (kube.ProwJob, error) {
	var prowJob kube.ProwJob
	data, err := json.Marshal(pj)
	if err != nil {
		return prowJob, err
	}

	if err := json.Unmarshal(data, &prowJob); err != nil {
		return prowJob, err
	}

	return prowJob, nil
}

// kubeEnv transforms a mapping of environment variables
// into their serialized form for a PodSpec
func kubeEnv(environment map[string]string) []kube.EnvVar {
	var kubeEnvironment []kube.EnvVar
	for key, value := range environment {
		kubeEnvironment = append(kubeEnvironment, kube.EnvVar{
			Name:  key,
			Value: value,
		})
	}

	return kubeEnvironment
}

// PartitionActive separates the provided prowjobs into pending and triggered
// and returns them inside channels so that they can be consumed in parallel
// by different goroutines. Complete prowjobs are filtered out. Controller
// loops need to handle pending jobs first so they can conform to maximum
// concurrency requirements that different jobs may have.
func PartitionActive(pjs []kube.ProwJob) (pending, triggered chan kube.ProwJob) {
	// Size channels correctly.
	pendingCount, triggeredCount := 0, 0
	for _, pj := range pjs {
		switch pj.Status.State {
		case kube.PendingState:
			pendingCount++
		case kube.TriggeredState:
			triggeredCount++
		}
	}
	pending = make(chan kube.ProwJob, pendingCount)
	triggered = make(chan kube.ProwJob, triggeredCount)

	// Partition the jobs into the two separate channels.
	for _, pj := range pjs {
		switch pj.Status.State {
		case kube.PendingState:
			pending <- pj
		case kube.TriggeredState:
			triggered <- pj
		}
	}
	close(pending)
	close(triggered)
	return pending, triggered
}

// GetLatestProwJobs filters through the provided prowjobs and returns
// a map of jobType jobs to their latest prowjobs.
func GetLatestProwJobs(pjs []kube.ProwJob, jobType kube.ProwJobType) map[string]kube.ProwJob {
	latestJobs := make(map[string]kube.ProwJob)
	for _, j := range pjs {
		if j.Spec.Type != jobType {
			continue
		}
		name := j.Spec.Job
		if j.Status.StartTime.After(latestJobs[name].Status.StartTime) {
			latestJobs[name] = j
		}
	}
	return latestJobs
}

// ProwJobFields extracts logrus fields from a prowjob useful for logging.
func ProwJobFields(pj *kube.ProwJob) logrus.Fields {
	fields := make(logrus.Fields)
	fields["name"] = pj.Metadata.Name
	fields["job"] = pj.Spec.Job
	fields["type"] = pj.Spec.Type
	if len(pj.Metadata.Labels[github.EventGUID]) > 0 {
		fields[github.EventGUID] = pj.Metadata.Labels[github.EventGUID]
	}
	if len(pj.Spec.Refs.Pulls) == 1 {
		fields[github.PrLogField] = pj.Spec.Refs.Pulls[0].Number
		fields[github.RepoLogField] = pj.Spec.Refs.Repo
		fields[github.OrgLogField] = pj.Spec.Refs.Org
	}
	return fields
}
