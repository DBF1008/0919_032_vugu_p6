package gen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// "github.com/vugu/vugu/internal/htmlx"
	// "golang.org/x/net/html"
	"github.com/vugu/html"
	"github.com/vugu/html/atom"
)

// compactDoc parses a full HTML document, runs compactNodeTree on it and
// returns the re-rendered result.
func compactDoc(t *testing.T, in string) string {
	t.Helper()
	n, err := html.Parse(strings.NewReader(in))
	require.NoError(t, err)
	require.NoError(t, compactNodeTree(n))
	var buf bytes.Buffer
	require.NoError(t, html.Render(&buf, n))
	return buf.String()
}

// compactFragment parses input the same way ParserGo.Parse does for component
// fragments (no <html> tag), runs compactNodeTree on each top level element
// and returns the re-rendered result.
func compactFragment(t *testing.T, in string) string {
	t.Helper()
	nlist, err := html.ParseFragment(strings.NewReader(in), &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Div,
		Data:     "div",
	})
	require.NoError(t, err)
	var buf bytes.Buffer
	for _, n := range nlist {
		if n.Type != html.ElementNode {
			continue
		}
		require.NoError(t, compactNodeTree(n))
		require.NoError(t, html.Render(&buf, n))
	}
	return buf.String()
}

func TestCompactNodeTree(t *testing.T) {

	assert := assert.New(t)

	var in = `<html>
<head></head>
<body>
	<div>
		<ul>
			<li vg-for="abc123" vg-html="something"></li>
		</ul>
		<p>
			This is some static text here, <strong>blah</strong> bleh <em>blee</em>.
		</p>
	</div>
</body>
</html>`

	out := compactDoc(t, in)

	// the static <p> must be compacted even though its sibling <ul> contains
	// a dynamic <li> (vg-for/vg-html) - previously nothing was compacted at all
	assert.Contains(out, "<p vg-html=\"vugu.HTML(&#34;")
	// the dynamic li must be left alone
	assert.Contains(out, `<li vg-for="abc123" vg-html="something"></li>`)
}

// Full HTML documents arrive as a Document node; make sure the walk descends
// into it and compacts the static contents of body.
func TestCompactFullHTMLDocument(t *testing.T) {

	assert := assert.New(t)

	out := compactDoc(t, `<html><head><title>hi</title></head><body><div><p>static</p><ul><li>a</li><li>b</li></ul></div></body></html>`)

	// static div inside body gets compacted
	assert.Contains(out, `<div vg-html="vugu.HTML(&#34;`)
	// html and body themselves must never receive vg-html
	assert.NotContains(out, `<html vg-html`)
	assert.NotContains(out, `<body vg-html`)
	// head and its contents are left untouched
	assert.Contains(out, `<head><title>hi</title></head>`)
}

// A node that is not itself static (e.g. has vg-if) but whose children are all
// static must still have its static children compacted.  Previously the
// all-or-nothing logic left everything unoptimized.
func TestCompactDynamicParentStaticChildren(t *testing.T) {

	assert := assert.New(t)

	out := compactDoc(t, `<html><head></head><body><div vg-if="c.Show"><p>static one</p><p>static <strong>two</strong></p></div></body></html>`)

	// both static <p> elements are compacted
	assert.Equal(2, strings.Count(out, "vg-html=\"vugu.HTML("))
	// the dynamic parent keeps its vg-if and does not get vg-html itself
	assert.Contains(out, `<div vg-if="c.Show">`)
	assert.NotContains(out, `<div vg-if="c.Show" vg-html`)
}

// When one sibling is not compactable, the other (compactable) siblings must
// still be optimized.
func TestCompactMixedSiblings(t *testing.T) {

	assert := assert.New(t)

	out := compactDoc(t, `<html><head></head><body><div><p>static</p><ul><li vg-for="c.Items">dyn</li></ul><span>also static</span></div></body></html>`)

	assert.Contains(out, `<p vg-html="vugu.HTML(&#34;static&#34;)"></p>`)
	assert.Contains(out, `<span vg-html="vugu.HTML(&#34;also static&#34;)"></span>`)
	assert.Contains(out, `<li vg-for="c.Items">dyn</li>`)
}

// A vg-comp tag (component reference without a colon prefix) must never be
// treated as a static element, otherwise the component would not render.
func TestCompactVGCompNotCompacted(t *testing.T) {

	assert := assert.New(t)

	// vg-comp as the only child of an otherwise fully static branch
	out := compactDoc(t, `<html><head></head><body><div><section><vg-comp expr="c.Body"></vg-comp></section></div></body></html>`)
	assert.Contains(out, `<vg-comp expr="c.Body"></vg-comp>`)
	// no ancestor swallowed the component into a static vg-html string;
	// in fact nothing on this branch may be compacted at all
	assert.NotContains(out, `vg-html`)

	// vg-comp mixed with static siblings: siblings compacted, component intact
	out = compactDoc(t, `<html><head></head><body><div><p>static</p><vg-comp expr="c.Body"></vg-comp></div></body></html>`)
	assert.Contains(out, `<p vg-html="vugu.HTML(&#34;static&#34;)"></p>`)
	assert.Contains(out, `<vg-comp expr="c.Body"></vg-comp>`)
}

// Colon-separated component references (pkg:Comp) must never be compacted.
func TestCompactColonComponentNotCompacted(t *testing.T) {

	assert := assert.New(t)

	out := compactDoc(t, `<html><head></head><body><div><p>static</p><main:Widget></main:Widget></div></body></html>`)
	assert.Contains(out, `<p vg-html="vugu.HTML(&#34;static&#34;)"></p>`)
	assert.Contains(out, `<main:Widget></main:Widget>`)
	assert.NotContains(out, `<main:Widget vg-html`)
}

// The common case of a component fragment with a single static root element:
// the root itself should be compacted (previously the return value of the
// walk was ignored and nothing happened at all).
func TestCompactFragmentRoot(t *testing.T) {

	assert := assert.New(t)

	out := compactFragment(t, `<div><p>static</p><ul><li>a</li></ul></div>`)
	assert.Contains(out, `<div vg-html="vugu.HTML(&#34;`)
	assert.NotContains(out, `<p>`)

	// fragment root with dynamic attribute: root not compacted, children are
	out = compactFragment(t, `<div vg-if="c.Show"><p>static</p></div>`)
	assert.Contains(out, `<div vg-if="c.Show">`)
	assert.Contains(out, `<p vg-html="vugu.HTML(&#34;static&#34;)"></p>`)
}

// head, script, style and vg-* tags (and everything inside them) are frozen.
func TestCompactFrozenTags(t *testing.T) {

	assert := assert.New(t)

	out := compactDoc(t, `<html><head><style>.a{color:red}</style><script type="text/javascript">var x = 1;</script></head><body><div><vg-template><p>in tmpl</p></vg-template></div></body></html>`)

	assert.Contains(out, `<style>.a{color:red}</style>`)
	assert.Contains(out, `var x = 1;`)
	// vg-template contents are not compacted
	assert.Contains(out, `<vg-template><p>in tmpl</p></vg-template>`)
}

// Elements with no contents must not get an empty vg-html attribute.
func TestCompactEmptyElement(t *testing.T) {

	assert := assert.New(t)

	out := compactFragment(t, `<div><span></span><br/></div>`)
	assert.Contains(out, `<div vg-html="vugu.HTML(&#34;`)
	// the empty span and br are serialized inside the parent's static HTML,
	// they do not carry their own vg-html
	assert.NotContains(out, `<span vg-html`)
	assert.NotContains(out, `<br vg-html`)
}

// Residual dynamic attributes (vg-*, :, ., @) on nodes being serialized as
// static HTML must be cleaned so they cannot conflict with generated code.
func TestStripDynamicAttrs(t *testing.T) {

	assert := assert.New(t)

	n, err := html.ParseFragment(strings.NewReader(`<div class="keep" vg-if="x" :href="y" @click="z" .prop="w"><span vg-html="vugu.HTML(&#34;x&#34;)" id="s">t</span></div>`), &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Div,
		Data:     "div",
	})
	require.NoError(t, err)
	require.Len(t, n, 1)

	stripDynamicAttrs(n[0])

	var buf bytes.Buffer
	require.NoError(t, html.Render(&buf, n[0]))
	out := buf.String()

	assert.Contains(out, `class="keep"`)
	assert.Contains(out, `id="s"`)
	assert.Contains(out, `>t</span>`)
	assert.NotContains(out, "vg-if")
	assert.NotContains(out, `href=`)
	assert.NotContains(out, "click")
	assert.NotContains(out, "prop")
	assert.NotContains(out, "vg-html")
}

// compactElement must not stack a second vg-html on an element that already
// has one (or a vg-content), otherwise two conflicting innerHTML expressions
// would be generated.
func TestCompactElementRemovesResidualVGHTML(t *testing.T) {

	assert := assert.New(t)

	n, err := html.ParseFragment(strings.NewReader(`<div vg-html="old"><p>static</p></div>`), &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Div,
		Data:     "div",
	})
	require.NoError(t, err)
	require.Len(t, n, 1)

	require.NoError(t, compactElement(n[0]))

	count := 0
	for _, a := range n[0].Attr {
		if a.Key == "vg-html" {
			count++
			assert.NotEqual("old", a.Val)
		}
		assert.NotEqual("vg-content", a.Key)
	}
	assert.Equal(1, count)
	assert.Nil(n[0].FirstChild)
	assert.Nil(n[0].LastChild)
}

func TestIsComponentElement(t *testing.T) {

	assert := assert.New(t)

	parse1 := func(in string) *html.Node {
		nlist, err := html.ParseFragment(strings.NewReader(in), &html.Node{
			Type:     html.ElementNode,
			DataAtom: atom.Div,
			Data:     "div",
		})
		require.NoError(t, err)
		require.Len(t, nlist, 1)
		return nlist[0]
	}

	assert.True(isComponentElement(parse1(`<vg-comp expr="c.X"></vg-comp>`)))
	assert.True(isComponentElement(parse1(`<main:Widget></main:Widget>`)))
	assert.True(isComponentElement(parse1(`<pkg:comp></pkg:comp>`)))
	assert.False(isComponentElement(parse1(`<div></div>`)))
	assert.False(isComponentElement(parse1(`<svg></svg>`)))
	assert.False(isComponentElement(&html.Node{Type: html.TextNode, Data: "x"}))
}

func TestIsStaticEl(t *testing.T) {

	assert := assert.New(t)

	mkEl := func(data string, attrs ...html.Attribute) *html.Node {
		return &html.Node{Type: html.ElementNode, Data: data, Attr: attrs}
	}

	// plain static element
	assert.True(isStaticEl(mkEl("div", html.Attribute{Key: "class", Val: "a"})))
	// components are not static
	assert.False(isStaticEl(mkEl("vg-comp", html.Attribute{Key: "expr", Val: "c.X"})))
	assert.False(isStaticEl(mkEl("main:widget")))
	// vg-* directive attributes make it dynamic
	assert.False(isStaticEl(mkEl("div", html.Attribute{Key: "vg-if", Val: "x"})))
	// bound attribute / property / event handler make it dynamic
	assert.False(isStaticEl(mkEl("div", html.Attribute{Key: ":href", OrigKey: ":href", Val: "x"})))
	assert.False(isStaticEl(mkEl("div", html.Attribute{Key: ".prop", OrigKey: ".prop", Val: "x"})))
	assert.False(isStaticEl(mkEl("div", html.Attribute{Key: "@click", OrigKey: "@click", Val: "x"})))
	// non-letter attribute start is assumed dynamic
	assert.False(isStaticEl(mkEl("div", html.Attribute{Key: "1abc", Val: "x"})))
	// non-elements are not static elements
	assert.False(isStaticEl(&html.Node{Type: html.TextNode, Data: "x"}))
}
