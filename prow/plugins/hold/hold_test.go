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

package hold

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sirupsen/logrus"

	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"
)

func TestHandle(t *testing.T) {
	var tests = []struct {
		name          string
		prAuthor      string
		commentAuthor string
		body          string
		hasLabel      bool
		shouldLabel   bool
		shouldUnlabel bool
		shouldComment bool
	}{
		{
			name:          "nothing to do",
			prAuthor:      "bob",
			commentAuthor: "bill",
			body:          "noise",
			hasLabel:      false,
			shouldLabel:   false,
			shouldUnlabel: false,
			shouldComment: false,
		},
		{
			name:          "author requested hold",
			prAuthor:      "bob",
			commentAuthor: "bob",
			body:          "/hold",
			hasLabel:      false,
			shouldLabel:   true,
			shouldUnlabel: false,
			shouldComment: false,
		},
		{
			name:          "author requested hold, label already exists",
			prAuthor:      "bob",
			commentAuthor: "bob",
			body:          "/hold",
			hasLabel:      true,
			shouldLabel:   false,
			shouldUnlabel: false,
			shouldComment: false,
		},
		{
			name:          "non-author requested hold",
			prAuthor:      "bob",
			commentAuthor: "bill",
			body:          "/hold",
			hasLabel:      false,
			shouldLabel:   false,
			shouldUnlabel: false,
			shouldComment: true,
		},
		{
			name:          "author requested hold cancel",
			prAuthor:      "bob",
			commentAuthor: "bob",
			body:          "/hold cancel",
			hasLabel:      true,
			shouldLabel:   false,
			shouldUnlabel: true,
			shouldComment: false,
		},
		{
			name:          "author requested hold cancel, label already gone",
			prAuthor:      "bob",
			commentAuthor: "bob",
			body:          "/hold cancel",
			hasLabel:      false,
			shouldLabel:   false,
			shouldUnlabel: false,
			shouldComment: false,
		},
		{
			name:          "non-author requested hold cancel",
			prAuthor:      "bob",
			commentAuthor: "bill",
			body:          "/hold cancel",
			hasLabel:      false,
			shouldLabel:   false,
			shouldUnlabel: false,
			shouldComment: true,
		},
	}

	for _, tc := range tests {
		fc := &fakegithub.FakeClient{
			IssueComments: make(map[int][]github.IssueComment),
		}

		org, repo, number, htmlurl := "org", "repo", 1, "url"
		e := &event{
			org:           org,
			repo:          repo,
			number:        number,
			prAuthor:      tc.prAuthor,
			commentAuthor: tc.commentAuthor,
			body:          tc.body,
			hasLabel:      tc.hasLabel,
			htmlurl:       htmlurl,
		}

		if err := handle(fc, logrus.WithField("plugin", pluginName), e); err != nil {
			t.Errorf("For case %s, didn't expect error from hold: %v", tc.name, err)
			continue
		}

		fakeLabel := fmt.Sprintf("%s/%s#%d:%s", org, repo, number, label)
		if tc.shouldLabel {
			if len(fc.LabelsAdded) != 1 || fc.LabelsAdded[0] != fakeLabel {
				t.Errorf("For case %s: expected to add %q label but instead added: %v", tc.name, label, fc.LabelsAdded)
			}
		} else if len(fc.LabelsAdded) > 0 {
			t.Errorf("For case %s, expected to not add %q label but added: %v", tc.name, label, fc.LabelsAdded)
		}
		if tc.shouldUnlabel {
			if len(fc.LabelsRemoved) != 1 || fc.LabelsRemoved[0] != fakeLabel {
				t.Errorf("For case %s: expected to remove %q label but instead removed: %v", tc.name, label, fc.LabelsRemoved)
			}
		} else if len(fc.LabelsRemoved) > 0 {
			t.Errorf("For case %s, expected to not remove %q label but removed: %v", tc.name, label, fc.LabelsRemoved)
		}

		if tc.shouldComment {
			if len(fc.IssueCommentsAdded) != 1 || !strings.Contains(fc.IssueCommentsAdded[0], warning) {
				t.Errorf("For case %s: expected to add comment but instead added: %v", tc.name, fc.IssueCommentsAdded)
			}
		} else if len(fc.IssueCommentsAdded) > 0 {
			t.Errorf("For case %s, expected to not add comment but added: %v", tc.name, fc.IssueCommentsAdded)
		}
	}
}
