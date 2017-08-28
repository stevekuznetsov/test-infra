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

// Package hold contains a plugin which will allow users to label their
// own pull requests as not ready or ready for merge. The submit queue
// will honor the label to ensure pull requests do not merge when it is
// applied.
package hold

import (
	"regexp"

	"github.com/Sirupsen/logrus"

	"fmt"

	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
)

const pluginName = "hold"

var (
	label         = "do-not-merge/hold"
	labelRe       = regexp.MustCompile(`(?mi)^/hold\s*$`)
	labelCancelRe = regexp.MustCompile(`(?mi)^/hold cancel\s*$`)
)

type event struct {
	org           string
	repo          string
	number        int
	body          string
	htmlurl       string
	hasLabel      bool
}

func init() {
	plugins.RegisterIssueCommentHandler(pluginName, handleIssueComment)
	plugins.RegisterReviewEventHandler(pluginName, handleReview)
	plugins.RegisterReviewCommentEventHandler(pluginName, handleReviewComment)
}

type githubClient interface {
	AddLabel(owner, repo string, number int, label string) error
	CreateComment(owner, repo string, number int, comment string) error
	RemoveLabel(owner, repo string, number int, label string) error
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
}

func handleIssueComment(pc plugins.PluginClient, ic github.IssueCommentEvent) error {
	// Only consider open PRs.
	if !ic.Issue.IsPullRequest() || ic.Issue.State != "open" || ic.Action != github.IssueCommentActionCreated {
		return nil
	}

	e := &event{
		org:           ic.Repo.Owner.Login,
		repo:          ic.Repo.Name,
		number:        ic.Issue.Number,
		body:          ic.Comment.Body,
		hasLabel:      ic.Issue.HasLabel(label),
		htmlurl:       ic.Comment.HTMLURL,
	}
	return handle(pc.GitHubClient, pc.Logger, e)
}

func handleReview(pc plugins.PluginClient, re github.ReviewEvent) error {
	if re.Action != github.ReviewActionSubmitted {
		return nil
	}

	var (
		org    = re.Repo.Owner.Login
		repo   = re.Repo.Name
		number = re.PullRequest.Number
	)

	hasLabel, err := github.HasLabel(pc.GitHubClient, org, repo, number, label)
	if err != nil {
		return fmt.Errorf("failed to get the labels on %s/%s#%d: %v", org, repo, number, err)
	}

	e := &event{
		org:           org,
		repo:          repo,
		number:        number,
		body:          re.Review.Body,
		hasLabel:      hasLabel,
		htmlurl:       re.Review.HTMLURL,
	}
	return handle(pc.GitHubClient, pc.Logger, e)
}

func handleReviewComment(pc plugins.PluginClient, rce github.ReviewCommentEvent) error {
	if rce.Action != github.ReviewCommentActionCreated {
		return nil
	}

	var (
		org    = rce.Repo.Owner.Login
		repo   = rce.Repo.Name
		number = rce.PullRequest.Number
	)

	hasLabel, err := github.HasLabel(pc.GitHubClient, org, repo, number, label)
	if err != nil {
		return fmt.Errorf("failed to get the labels on %s/%s#%d: %v", org, repo, number, err)
	}

	e := &event{
		org:           org,
		repo:          repo,
		number:        number,
		body:          rce.Comment.Body,
		hasLabel:      hasLabel,
		htmlurl:       rce.Comment.HTMLURL,
	}
	return handle(pc.GitHubClient, pc.Logger, e)
}

// handle drives the pull request to the desired state. If any user adds
// a /hold directive, we want to add a label if one does not already exist.
// If they add /hold cancel, we want to remove the label if it exists.
func handle(gc githubClient, log *logrus.Entry, e *event) error {
	needsLabel := false
	if labelRe.MatchString(e.body) {
		needsLabel = true
	} else if labelCancelRe.MatchString(e.body) {
		needsLabel = false
	} else {
		return nil
	}

	if e.hasLabel && !needsLabel {
		log.Info("Removing %q label for %s/%s#%d", label, e.org, e.repo, e.number)
		return gc.RemoveLabel(e.org, e.repo, e.number, label)
	} else if !e.hasLabel && needsLabel {
		log.Info("Adding %q label for %s/%s#%d", label, e.org, e.repo, e.number)
		return gc.AddLabel(e.org, e.repo, e.number, label)
	}
	return nil
}
