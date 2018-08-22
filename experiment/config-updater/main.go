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

// config-updater watches for merged PRs which update a set of files
// and update the corresponding files in a given deployment
package main

import (
	"flag"
	"net/http"
	"net/url"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/config"

	"k8s.io/test-infra/prow/git"
	"k8s.io/test-infra/prow/github"
)

var (
	port              = flag.Int("port", 8888, "Port to listen on.")
	dryRun            = flag.Bool("dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	githubEndpoint    = flag.String("github-endpoint", "https://api.github.com", "GitHub's API endpoint.")
	githubTokenFile   = flag.String("github-token-file", "/etc/github/oauth", "Path to the file containing the GitHub OAuth secret.")
	webhookSecretFile = flag.String("hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	updateConfigFile  = flag.String("update-config-file", "/etc/config/update.yaml", "Path to the file containing the configurations to update.")
)

func main() {
	flag.Parse()
	logrus.SetFormatter(&logrus.JSONFormatter{})
	// Ignore SIGTERM so that we don't drop hooks when the pod is removed.
	// We'll get SIGTERM first and then SIGKILL after our graceful termination
	// deadline.
	signal.Ignore(syscall.SIGTERM)

	configAgent := &Agent{}
	if err := configAgent.Start(*updateConfigFile); err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	secretAgent := &config.SecretAgent{}
	if err := secretAgent.Start([]string{*webhookSecretFile, *githubTokenFile}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	if _, err := url.Parse(*githubEndpoint); err != nil {
		logrus.WithError(err).Fatal("Must specify a valid --github-endpoint URL.")
	}

	githubClient := github.NewClient(secretAgent.GetTokenGenerator(*githubTokenFile), *githubEndpoint)
	if *dryRun {
		githubClient = github.NewDryRunClient(secretAgent.GetTokenGenerator(*githubTokenFile), *githubEndpoint)
	}

	gitClient, err := git.NewClient()
	if err != nil {
		logrus.WithError(err).Fatal("Error getting git client.")
	}

	server := NewServer(secretAgent.GetTokenGenerator(*webhookSecretFile), gitClient, githubClient, configAgent)

	http.Handle("/", server)
	logrus.Fatal(http.ListenAndServe(":"+strconv.Itoa(*port), nil))
}
