// Command omitemptyscalar flags a `,omitempty` tag on a bool field, where a
// false is dropped on write. It exits 3 when it reports a finding. A repo runs
// it in its audit gate:
//
//	go run github.com/dkoosis/repo-standard/cmd/omitemptyscalar@<version> ./...
//
// A deliberate case takes `//nolint:omitemptyscalar // reason` on the field.
// The gate is described in repo-standard's README, section "Rules the audit
// and lint standard adds".
package main

import (
	"github.com/dkoosis/repo-standard/internal/omitemptyscalar"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(omitemptyscalar.Analyzer)
}
