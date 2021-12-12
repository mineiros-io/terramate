// Copyright 2021 Mineiros GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package test

import (
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/hcl"
)

func AssertTerramateBlock(t *testing.T, got, want hcl.Terramate) {
	t.Helper()

	assert.EqualStrings(t, got.RequiredVersion, want.RequiredVersion)
	assert.EqualInts(t, len(got.After), len(want.After), "After length mismatch")

	for i, w := range want.After {
		assert.EqualStrings(t, w, got.After[i], "stack mismatch")
	}

	if (want.Backend == nil) != (got.Backend == nil) {
		t.Fatalf("want.Backend[%+v] != got.Backend[%+v]",
			want.Backend, got.Backend)
	}

	if want.Backend != nil {
		assert.EqualStrings(t, want.Backend.Type, got.Backend.Type, "type differs")
		assert.EqualInts(t, len(want.Backend.Labels), len(got.Backend.Labels), "labels length")
		for i, wl := range want.Backend.Labels {
			assert.EqualStrings(t, wl, got.Backend.Labels[i], "label differ")
		}

		// TODO(i4k): compare the rest?
	}

}
