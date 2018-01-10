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

package refs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"k8s.io/test-infra/prow/pjutil"
)

// Resolve will determine the Refs being tested in
// by parsing Prow environment variable contents
func Resolve() (*pjutil.JobSpec, error) {
	specEnv, ok := os.LookupEnv("JOB_SPEC")
	if !ok {
		return nil, errors.New("$JOB_SPEC unset")
	}

	spec := &pjutil.JobSpec{}
	if err := json.Unmarshal([]byte(specEnv), spec); err != nil {
		return nil, fmt.Errorf("malformed $JOB_SPEC: %v", err)
	}

	return spec, nil
}
