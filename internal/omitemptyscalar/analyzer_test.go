package omitemptyscalar_test

import (
	"testing"

	"github.com/dkoosis/repo-standard/internal/omitemptyscalar"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), omitemptyscalar.Analyzer, "wire")
}
