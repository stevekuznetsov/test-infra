package github

type labelLister interface {
	GetIssueLabels(org, repo string, number int) ([]Label, error)
}

// HasLabel checks if a label is applied to a pr.
func HasLabel(c labelLister, org, repo string, num int, label string) (bool, error) {
	labels, err := c.GetIssueLabels(org, repo, num)
	if err != nil {
		return false, err
	}
	for _, candidate := range labels {
		if candidate.Name == label {
			return true, nil
		}
	}
	return false, nil
}
