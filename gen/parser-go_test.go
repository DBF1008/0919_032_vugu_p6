package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vugu/html"
)

// parseToGo is a helper that runs ParserGo.Parse on the given input and
// returns the generated Go source code.
func parseToGo(t *testing.T, in string, noOptimizeStatic bool) string {
	t.Helper()
	dir := t.TempDir()
	pg := &ParserGo{
		PackageName:      "main",
		StructType:       "Root",
		OutDir:           dir,
		OutFile:          "root_gen.go",
		NoOptimizeStatic: noOptimizeStatic,
	}
	require.NoError(t, pg.Parse(strings.NewReader(in), "root.vugu"))
	b, err := os.ReadFile(filepath.Join(dir, "root_gen.go"))
	require.NoError(t, err)
	return string(b)
}

// End to end test for the static HTML optimization on a full HTML document:
// static fragments must become vg-html (SetInnerHTML) expressions, including
// static content nested inside dynamic (vg-if) elements, while components
// (vg-comp) must still be emitted as components so they actually render.
func TestParseOptimizesStaticHTML(t *testing.T) {

	assert := assert.New(t)

	in := `<html>
<head><title>probe</title></head>
<body>
<div>
	<p>static paragraph</p>
	<div vg-if="c.Show"><span>nested static</span></div>
	<vg-comp expr="c.Body"></vg-comp>
</div>
</body>
</html>`

	out := parseToGo(t, in, false)

	// static content is compacted into vg-html expressions
	assert.Contains(out, `SetInnerHTML(vugu.HTML("static paragraph"))`)
	// static fragment nested inside a dynamic (vg-if) element is compacted too
	assert.Contains(out, `if c.Show {`)
	assert.Contains(out, `SetInnerHTML(vugu.HTML("nested static"))`)
	// the vg-comp component is emitted as a component, not baked into static HTML
	assert.Contains(out, `var vgcomp vugu.Builder = c.Body`)
	assert.Contains(out, `vgin.BuildEnv.WireComponent(vgcomp)`)

	// with the optimization disabled no vg-html output is generated,
	// but the component is still emitted
	out = parseToGo(t, in, true)
	assert.NotContains(out, "SetInnerHTML")
	assert.Contains(out, `var vgcomp vugu.Builder = c.Body`)
}

// A single-root-element fragment that is fully static gets its root element
// compacted as well.
func TestParseOptimizesStaticFragmentRoot(t *testing.T) {

	assert := assert.New(t)

	out := parseToGo(t, `<div><p>static</p></div>`, false)
	// note: htmlGoQuoteString escapes < and > as \x3C and \x3E
	assert.Contains(out, `SetInnerHTML(vugu.HTML("\x3Cp\x3Estatic\x3C/p\x3E"))`)
}

func TestEmitForExpr(t *testing.T) {
	tests := []struct {
		name           string
		node           *html.Node
		expectedError  string
		expectedResult string
	}{
		{
			name:          "no vg-for attributes",
			node:          &html.Node{},
			expectedError: "no for expression, code should not be calling emitForExpr when no vg-for is present",
		},
		{
			name: "no iteration vars",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "c.Items"},
				},
			},
			// WARNING these tests are very brittle. The new line is required.
			expectedResult: `for key, value := range c.Items {
_ = key
_ = value
`,
		},
		{
			name: "no iteration vars with vg-key",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "c.Items"},
					{Key: "vg-key", Val: "1"},
				},
			},
			expectedResult: `for key, value := range c.Items {
_ = key
_ = value
`,
		},
		{
			name: "key and value vars",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "k, v := range c.Items"},
				},
			},
			expectedResult: `for k, v := range c.Items {
`,
		},
		{
			name: "only key var",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "k := range c.Items"},
				},
			},
			expectedResult: `for k := range c.Items {
`,
		},
		{
			name: "only key var with vg-key",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "k := range c.Items"},
					{Key: "vg-key", Val: "1"},
				},
			},
			expectedResult: `for k := range c.Items {
`,
		},
		{
			name: "only value var",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "_, v := range c.Items"},
				},
			},
			expectedResult: `for _, v := range c.Items {
`,
		},
		{
			name: "only value var with vg-key",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "_, v := range c.Items"},
					{Key: "vg-key", Val: "1"},
				},
			},
			expectedResult: `for _, v := range c.Items {
`,
		},
		{
			name: "iteration with for clause",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "i:= 0; i < 5; i++"},
				},
			},
			expectedResult: `for i:= 0; i < 5; i++ {
`,
		},
		{
			name: "iteration with for clause with vg-key",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "i:= 0; i < 5; i++"},
					{Key: "vg-key", Val: "1"},
				},
			},
			expectedResult: `for i:= 0; i < 5; i++ {
`,
		},
		{
			name: "iteration with for clause with vg-key",
			node: &html.Node{
				Attr: []html.Attribute{
					{Key: "vg-for", Val: "i:= 0; i < 5; i++"},
					{Key: "vg-key", Val: "1"},
				},
			},
			expectedResult: `for i:= 0; i < 5; i++ {
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)
			pg := &ParserGo{}
			state := &parseGoState{}

			err := pg.emitForExpr(state, tt.node)

			if tt.expectedError != "" {
				require.EqualError(err, tt.expectedError)
				return
			}
			require.NoError(err)
			assert.Exactly(tt.expectedResult, state.buildBuf.String())
		})
	}
}
