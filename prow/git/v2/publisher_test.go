/*
Copyright 2019 The Kubernetes Authors.

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

package git

import (
	"errors"
	"github.com/sirupsen/logrus"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func TestPublisher_Commit(t *testing.T) {
	var testCases = []struct {
		name          string
		title, body   string
		info          func() (string, string, error)
		responses     map[string]execResponse
		expectedCalls [][]string
		expectedErr   bool
	}{
		{
			name:  "no errors works fine",
			title: "title",
			body:  `body\nmore`,
			info: func() (string, string, error) {
				return "robot", "boop@beep.zoop", nil
			},
			responses: map[string]execResponse{
				"add --all": {
					out: []byte("ok"),
				},
				`commit --message title --message body\nmore --author robot <boop@beep.zoop>`: {
					out: []byte("ok"),
				},
			},
			expectedCalls: [][]string{
				{"add", "--all"},
				{"commit", "--message", "title", "--message", `body\nmore`, "--author", "robot <boop@beep.zoop>"},
			},
			expectedErr: false,
		},
		{
			name:  "add fails",
			title: "title",
			body:  `body\nmore`,
			info: func() (string, string, error) {
				return "robot", "boop@beep.zoop", nil
			},
			responses: map[string]execResponse{
				"add --all": {
					err: errors.New("oops"),
				},
			},
			expectedCalls: [][]string{
				{"add", "--all"},
			},
			expectedErr: true,
		},
		{
			name:  "info fails",
			title: "title",
			body:  `body\nmore`,
			info: func() (string, string, error) {
				return "", "", errors.New("oops")
			},
			responses:     map[string]execResponse{},
			expectedCalls: [][]string{},
			expectedErr:   true,
		},
		{
			name:  "commit fails",
			title: "title",
			body:  `body\nmore`,
			info: func() (string, string, error) {
				return "robot", "boop@beep.zoop", nil
			},
			responses: map[string]execResponse{
				"add --all": {
					out: []byte("ok"),
				},
				`commit --message title --message body\nmore --author robot <boop@beep.zoop>`: {
					err: errors.New("oops"),
				},
			},
			expectedCalls: [][]string{
				{"add", "--all"},
				{"commit", "--message", "title", "--message", `body\nmore`, "--author", "robot <boop@beep.zoop>"},
			},
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: testCase.responses,
			}
			p := publisher{
				executor: &e,
				info:     testCase.info,
				logger:   logrus.WithField("test", testCase.name),
			}
			actualErr := p.Commit(testCase.title, testCase.body)
			if testCase.expectedErr && actualErr == nil {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if !testCase.expectedErr && actualErr != nil {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, actualErr)
			}
			if actual, expected := e.records, testCase.expectedCalls; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect git calls: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestPublisher_ForcePush(t *testing.T) {
	var testCases = []struct {
		name          string
		branch        string
		remote        string
		resolveErr    error
		responses     map[string]execResponse
		expectedCalls [][]string
		expectedErr   bool
	}{
		{
			name:   "no errors works fine",
			branch: "master",
			remote: "http.com",
			responses: map[string]execResponse{
				"push --force http.com master": {
					out: []byte("ok"),
				},
			},
			expectedCalls: [][]string{
				{"push", "--force", "http.com", "master"},
			},
			expectedErr: false,
		},
		{
			name:          "error resolving remote makes no calls",
			branch:        "master",
			resolveErr:    errors.New("oops"),
			expectedCalls: [][]string{},
			expectedErr:   true,
		},
		{
			name:   "errors pushing propagates",
			branch: "master",
			remote: "http.com",
			responses: map[string]execResponse{
				"push --force http.com master": {
					err: errors.New("oops"),
				},
			},
			expectedCalls: [][]string{
				{"push", "--force", "http.com", "master"},
			},
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: testCase.responses,
			}
			r := fakeResolver{
				out: testCase.remote,
				err: testCase.resolveErr,
			}
			p := publisher{
				executor: &e,
				remote:   r.Resolve,
				logger:   logrus.WithField("test", testCase.name),
			}
			actualErr := p.ForcePush(testCase.branch)
			if testCase.expectedErr && actualErr == nil {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if !testCase.expectedErr && actualErr != nil {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, actualErr)
			}
			if actual, expected := e.records, testCase.expectedCalls; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect git calls: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}
