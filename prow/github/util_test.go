package github

import (
	"testing"

	"k8s.io/test-infra/prow/github/fakegithub"
)

func TestHasLabel(t *testing.T) {
	testCases := []struct {
		name       string
		labels     []string
		check      string
		shouldPass bool
	}{
		{
			name:       "no labels",
			labels:     []string{},
			check:      "good-pr",
			shouldPass: false,
		},
		{
			name:       "no labels, check empty",
			labels:     []string{},
			check:      "",
			shouldPass: false,
		},
		{
			name:       "some labels, check empty",
			labels:     []string{"some-label", "lgtm"},
			check:      "",
			shouldPass: false,
		},
		{
			name:       "some labels, does not have check",
			labels:     []string{"some-label", "definitely-not-lgtm"},
			check:      "good-pr",
			shouldPass: false,
		},
		{
			name:       "has label last",
			labels:     []string{"some-label", "definitely-not-lgtm", "good-pr"},
			check:      "good-pr",
			shouldPass: true,
		},
		{
			name:       "has label first",
			labels:     []string{"good-pr", "some-label", "definitely-not-lgtm"},
			check:      "good-pr",
			shouldPass: true,
		},
	}
	for _, test := range testCases {
		fc := &fakegithub.FakeClient{}
		for _, label := range test.labels {
			fc.AddLabel("k8s", "kuber", 42, label)
		}
		res, err := HasLabel(fc, "k8s", "kuber", 42, test.check)
		if err != nil {
			t.Errorf("Unexpected error from prLabelChecker on test '%s': %v.", test.name, err)
			continue
		}
		if res != test.shouldPass {
			var not string
			if !test.shouldPass {
				not = "not "
			}
			t.Errorf(
				"Error on test '%s': %q should %sbe recognized as a label on #42 which has labels %q.",
				test.name,
				test.check,
				not,
				test.labels,
			)
		}
	}
}
