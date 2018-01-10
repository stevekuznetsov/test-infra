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

package gcs

import (
	"fmt"
	"path"
	"strconv"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pjutil"
)

// PathForSpec determines the GCS path prefix for files uploaded
// for a specific job spec
func PathForSpec(spec *pjutil.JobSpec, pathSegment RepoPathBuilder) string {
	switch spec.Type {
	case kube.PeriodicJob, kube.PostsubmitJob:
		return path.Join("logs", spec.Job, spec.BuildId)
	case kube.PresubmitJob:
		return path.Join("pr-logs", "pull", pathSegment(spec.Refs.Org, spec.Refs.Repo), strconv.Itoa(spec.Refs.Pulls[0].Number), spec.Job, spec.BuildId)
	case kube.BatchJob:
		return path.Join("pr-logs", "pull", "batch", spec.Job, spec.BuildId)
	default:
		logrus.Fatalf("unknown job spec type: %v", spec.Type)
	}
	return ""
}

// AliasForSpec determines the GCS path aliases for a job spec
func AliasForSpec(spec *pjutil.JobSpec) string {
	switch spec.Type {
	case kube.PeriodicJob, kube.PostsubmitJob, kube.BatchJob:
		return ""
	case kube.PresubmitJob:
		return path.Join("pr-logs", "directory", spec.Job, spec.BuildId)
	default:
		logrus.Fatalf("unknown job spec type: %v", spec.Type)
	}
	return ""
}

type RepoPathBuilder func(org, repo string) string

// NewLegacyRepoPathBuilder returns a builder that handles the legacy path
// encoding where a path will only contain an org or repo if they are non-default
func NewLegacyRepoPathBuilder(defaultOrg, defaultRepo string) RepoPathBuilder {
	return func(org, repo string) string {
		if org == defaultOrg {
			if repo == defaultRepo {
				return ""
			}
			return repo
		} else {
			return fmt.Sprintf("%s_%s", org, repo)
		}
	}
}

// NewSingleDefaultRepoPathBuilder returns a builder that handles the legacy path
// encoding where a path will contain org and repo for all but one default repo
func NewSingleDefaultRepoPathBuilder(defaultOrg, defaultRepo string) RepoPathBuilder {
	return func(org, repo string) string {
		if org == defaultOrg && repo == defaultRepo {
			return ""
		} else {
			return fmt.Sprintf("%s_%s", org, repo)
		}
	}
}

// NewExplicitRepoPathBuilder returns a builder that handles the path encoding
// where a path will always have an explicit "org_repo" path segment
func NewExplicitRepoPathBuilder() RepoPathBuilder {
	return func(org, repo string) string {
		return fmt.Sprintf("%s_%s", org, repo)
	}
}
