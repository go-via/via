// Package tutorial_test checks the /tutorial programs. Each step is a package
// main of its own, so nothing imports them and only a build proves they run.
package tutorial_test

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var blank = regexp.MustCompile(`\n\s*\n`)

var steps = []string{"step1", "step2", "step3", "step4", "step5"}

func TestSteps_build(t *testing.T) {
	t.Parallel()
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			out, err := exec.Command("go", "build", "-o", os.DevNull, "./"+step).CombinedOutput()
			assert.NoError(t, err, "%s", out)
		})
	}
}

// The page tells readers the last step is the repo's chat example minus its
// comments; this keeps that true when either file changes.
func TestLastStep_matchesTheChatExampleWithoutComments(t *testing.T) {
	t.Parallel()
	assert.Equal(t, code(t, "../../../../example/chat/main.go"), code(t, steps[len(steps)-1]+"/main.go"))
}

// code prints a file's syntax tree, which drops its comments and normalises
// the alignment they would otherwise shift. Blank lines go too: a dropped
// comment line can leave one behind.
func code(t *testing.T, path string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)
	var b bytes.Buffer
	require.NoError(t, printer.Fprint(&b, fset, f))
	return blank.ReplaceAllString(b.String(), "\n")
}
