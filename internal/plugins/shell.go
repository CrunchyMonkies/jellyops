/*
Copyright 2026.

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

package plugins

import (
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// shellQuote wraps s in single quotes, escaping embedded single quotes, so it is
// safe to embed in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin quotes and space-joins tokens into a single shell command line.
func shellJoin(tokens []string) string {
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = shellQuote(t)
	}
	return strings.Join(quoted, " ")
}

// resourceQuantityOne returns the quantity "1", used for nvidia.com/gpu limits.
func resourceQuantityOne() resource.Quantity {
	return resource.MustParse("1")
}
