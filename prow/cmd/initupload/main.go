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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io/ioutil"
	"path"
	"time"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"
	"k8s.io/test-infra/prow/pod-utils/clone"

	"k8s.io/test-infra/prow/pod-utils/gcs"
	"k8s.io/test-infra/prow/pod-utils/refs"
)

var (
	cloneLog = flag.String("clone-log", "", "path to the output file for the cloning step")

	subDir = flag.String("sub-dir", "", "Optional sub-directory of the job's path to which artifacts are uploaded")

	pathStrategy = flag.String("path-strategy", "explicit", "how to encode org and repo into GCS paths")
	defaultOrg   = flag.String("default-org", "", "optional default org for GCS path encoding")
	defaultRepo  = flag.String("default-repo", "", "optional default repo for GCS path encoding")

	gcsBucket          = flag.String("gcs-bucket", "", "GCS bucket to upload into")
	gceCredentialsFile = flag.String("gcs-credentials-file", "", "file where Google Cloud authentication credentials are stored")
	dryRun             = flag.Bool("dry-run", true, "do not interact with GCS")
)

func main() {
	flag.Parse()

	if !*dryRun {
		if *gcsBucket != "" {
			logrus.Fatal("No GCS bucket specified")
		}

		if *gceCredentialsFile != "" {
			logrus.Fatal("No GCE credentials specified")
		}
	}

	if *pathStrategy != "legacy" && *pathStrategy != "explicit" && *pathStrategy != "single" {
		logrus.Fatal("Path strategy must be one of 'legacy', 'explicit', or 'single'")
	}

	if *pathStrategy != "explicit" && (*defaultOrg == "" || *defaultRepo == "") {
		logrus.Fatalf("Default org and repo must be provided for strategy %q", *pathStrategy)
	}

	var builder gcs.RepoPathBuilder
	switch *pathStrategy {
	case "explicit":
		builder = gcs.NewExplicitRepoPathBuilder()
	case "legacy":
		builder = gcs.NewLegacyRepoPathBuilder(*defaultOrg, *defaultRepo)
	case "single":
		builder = gcs.NewSingleDefaultRepoPathBuilder(*defaultOrg, *defaultRepo)
	}
	gcsPath := gcs.PathForSpec(refs.Resolve(), builder)
	if *subDir != "" {
		gcsPath = path.Join(gcsPath, *subDir)
	}

	var cloneRecords []clone.Record
	data, err := ioutil.ReadFile(*cloneLog)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read clone log")
	}
	err = json.Unmarshal(data, &cloneRecords)
	if err != nil {
		logrus.WithError(err).Fatal("Could not unmarshal clone records")
	}

	failed := false
	buildLog := bytes.Buffer{}
	for _, record := range cloneRecords {
		buildLog.WriteString(clone.FormatRecord(record))
		failed = failed || record.Failed
	}

	uploadTargets := map[string]gcs.UploadFunc{
		path.Join(gcsPath, "build-log.txt"): gcs.DataUpload(&buildLog),
	}

	started := struct {
		Timestamp int64 `json:"timestamp"`
	}{
		Timestamp: time.Now().Unix(),
	}
	startedData, err := json.Marshal(&started)
	if err != nil {
		logrus.WithError(err).Fatal("Could not marshal starting data")
	} else {
		uploadTargets[path.Join(gcsPath, "started.json")] = gcs.DataUpload(bytes.NewBuffer(startedData))
	}

	if failed {
		finished := struct {
			Timestamp int64 `json:"timestamp"`
			Passed    bool  `json:"passed"`
		}{
			Timestamp: time.Now().Unix(),
			Passed:    false,
		}
		finishedData, err := json.Marshal(&finished)
		if err != nil {
			logrus.WithError(err).Fatal("Could not marshal finishing data")
		} else {
			uploadTargets[path.Join(gcsPath, "finished.json")] = gcs.DataUpload(bytes.NewBuffer(finishedData))
		}
	}

	if !*dryRun {
		ctx := context.Background()
		gcsClient, err := storage.NewClient(ctx, option.WithCredentialsFile(*gceCredentialsFile))
		if err != nil {
			logrus.WithError(err).Fatal("Could not connect to GCS")
		}

		if err := gcs.UploadToGcs(gcsClient.Bucket(*gcsBucket), uploadTargets); err != nil {
			logrus.WithError(err).Fatal("Failed to upload to GCS")
		}
	}
}
