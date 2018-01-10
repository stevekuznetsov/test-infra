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
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	"k8s.io/test-infra/prow/pod-utils/gcs"
	"k8s.io/test-infra/prow/pod-utils/refs"
)

var (
	processLog = flag.String("process-log", "", "path to the log where stdout and stderr are streamed for the process we execute")
	markerFile = flag.String("marker-file", "", "file we write the return code of the process we execute once it has finished running")

	artifactDir = flag.String("artifact-dir", "", "directory on the filesystem to upload to GCS")
	subDir      = flag.String("sub-dir", "", "Optional sub-directory of the job's path to which artifacts are uploaded")

	pathStrategy = flag.String("path-strategy", "explicit", "how to encode org and repo into GCS paths")
	defaultOrg   = flag.String("default-org", "", "optional default org for GCS path encoding")
	defaultRepo  = flag.String("default-repo", "", "optional default repo for GCS path encoding")

	gcsBucket          = flag.String("gcs-bucket", "", "GCS bucket to upload into")
	gceCredentialsFile = flag.String("gcs-credentials-file", "", "file where Google Cloud authentication credentials are stored")
	dryRun             = flag.Bool("dry-run", true, "do not interact with GCS")
)

func main() {
	flag.Parse()
	if *processLog == "" {
		logrus.Fatal("No path for a process log file specified")
	}

	if *markerFile == "" {
		logrus.Fatal("No path for a marker file specified")
	}

	if *artifactDir == "" {
		logrus.Fatal("No path to the artifact directory specified")
	}

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

	// Only start watching file events if the file doesn't exist
	// If the file exists, it means the main process already completed.
	if _, err := os.Stat(*markerFile); os.IsNotExist(err) {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			logrus.WithError(err).Fatal("Could not begin fsnotify watch")
		}
		defer watcher.Close()

		group := sync.WaitGroup{}

		group.Add(1)
		go func() {
			defer group.Done()
			for {
				select {
				case event := <-watcher.Events:
					if event.Name == *markerFile && event.Op&fsnotify.Create == fsnotify.Create {
						return
					}
				case err := <-watcher.Errors:
					logrus.WithError(err).Info("Encountered an error during fsnotify watch")
				}
			}
		}()

		dir := filepath.Dir(*markerFile)
		if err := watcher.Add(dir); err != nil {
			logrus.WithError(err).Fatal("Could not add to fsnotify watch")
		}
		group.Wait()
	}

	passed := false
	returnCodeData, err := ioutil.ReadFile(*markerFile)
	if err != nil {
		logrus.WithError(err).Warn("Could not read return code from marker file")
	} else {
		returnCode, err := strconv.Atoi(strings.TrimSpace(string(returnCodeData)))
		if err != nil {
			logrus.WithError(err).Warn("Failed to parse process return code")
		}
		passed = returnCode == 0 && err == nil
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

	uploadTargets := map[string]gcs.UploadFunc{
		path.Join(gcsPath, "build-log.txt"): gcs.FileUpload(*processLog),
	}

	finished := struct {
		Timestamp int64 `json:"timestamp"`
		Passed    bool  `json:"passed"`
	}{
		Timestamp: time.Now().Unix(),
		Passed:    passed,
	}
	finishedData, err := json.Marshal(&finished)
	if err != nil {
		logrus.WithError(err).Warn("Could not marshal finishing data")
	} else {
		uploadTargets[path.Join(gcsPath, "finished.json")] = gcs.DataUpload(bytes.NewBuffer(finishedData))
	}

	filepath.Walk(*artifactDir, func(src string, info os.FileInfo, err error) error {
		if info == nil || info.IsDir() {
			return nil
		}

		// we know path will be below artifactDir, but we can't
		// communicate that to the filepath module. We can ignore
		// this error as we can be certain it won't occur and best-
		// effort upload is OK in any case
		if relPath, err := filepath.Rel(*artifactDir, src); err == nil {
			logrus.WithField("src", src).WithField("dest", relPath).Info("Uploading file")
			uploadTargets[path.Join(gcsPath, "artifacts", relPath)] = gcs.FileUpload(src)
		} else {
			logrus.WithField("src", src).WithError(err).Warn("Encountered error in relative path calculation")
		}
		return nil
	})

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
