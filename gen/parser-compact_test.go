package gen

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	// "github.com/vugu/vugu/internal/htmlx"
	// "golang.org/x/net/html"
	"github.com/vugu/html"
	"github.com/vugu/html/atom"
)

func compactTestParse(t *testing.T, in string) *html.Node {
	t.Helper()
	n, err := html.Parse(bytes.NewReader([]byte(in)))
	require.NoError(t, err)
	require.NoError(t, compactNodeTree(n))
	return n
}

func compactTestRender(t *testing.T, n *html.Node) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, html.Render(&buf, n))
	return buf.String()
}

func compactTestRun(t *testing.T, in string) string {
	return compactTestRender(t, compactTestParse(t, in))
}

// countVGHTML counts the vg-html attributes in rendered output.
func countVGHTML(t *testing.T, n *html.Node) int {
	t.Helper()
	out := compactTestRender(t, n)
	return strings.Count(out, "vg-html=")
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

	out := compactTestRun(t, in)

	log.Printf("OUT:\n%s", out)

	assert.Contains(out, "<p vg-html=\"vugu.HTML(&#34;")
	// the dynamic li keeps its original vg-html expression untouched
	assert.Contains(out, `<li vg-for="abc123" vg-html="something"></li>`)
}

// A static fragment nested inside a non-compactable element must still be
// compacted; previously the whole branch under the dynamic element was left
// untouched as soon as any single descendant was non-compactable.
func TestCompactNodeTreeStaticFragmentsInsideDynamic(t *testing.T) {

	in := `<html><head></head><body>
		<div vg-if="c.Show">
			<div>
				<p>hello</p><span>world</span>
			</div>
			<section>static section</section>
		</div>
	</body></html>`

	n := compactTestParse(t, in)
	out := compactTestRender(t, n)

	// the barrier div with vg-if keeps its identity
	assert.Contains(t, out, `<div vg-if="c.Show">`)
	// its static children were each compacted independently
	assert.Contains(t, out, `<div vg-html="vugu.HTML(`)
	assert.Contains(t, out, `<section vg-html="vugu.HTML(`)
	// and the static contents only survive inside the vg-html values,
	// not as live child nodes
	assert.NotContains(t, out, "<p>hello</p>")
	assert.NotContains(t, out, "<span>world</span>")

	// multiple levels of nesting: static nodes two barriers deep are still
	// compacted by their nearest static ancestor
	in2 := `<html><head></head><body>
		<div vg-if="c.A"><article><section>
			<header>head text</header><footer>foot text</footer>
		</section></article></div>
	</body></html>`
	out2 := compactTestRun(t, in2)
	// the deepest fully-static ancestor (<article>) folds the header/footer
	assert.Contains(t, out2, `<article vg-html="vugu.HTML(`)
	assert.NotContains(t, out2, "<header>head text</header>")
	assert.NotContains(t, out2, "<footer>foot text</footer>")
}

// A static sibling next to a non-compactable sibling must be compacted
// directly, instead of being kept because one of its siblings is dynamic.
func TestCompactNodeTreeMixedSiblings(t *testing.T) {

	in := `<html><head></head><body><div>
		<p>first static</p>
		<div vg-html="c.Dynamic"></div>
		<p>second static</p>
	</div></body></html>`

	out := compactTestRun(t, in)

	assert.Contains(t, out, `<p vg-html="vugu.HTML(`)
	assert.Contains(t, out, `<div vg-html="c.Dynamic"></div>`)
	assert.NotContains(t, out, "<p>first static</p>")
	assert.NotContains(t, out, "<p>second static</p>")
}

// vg-comp elements are components and must never be compacted or swallowed by
// a parent's vg-html value; their static siblings still must be optimized.
func TestCompactNodeTreeVGComp(t *testing.T) {

	in := `<html><head></head><body><div>
		<vg-comp expr="c.Foo"></vg-comp>
		<p>static paragraph</p>
	</div></body></html>`

	n := compactTestParse(t, in)
	out := compactTestRender(t, n)

	// the vg-comp survives with its expr attribute intact
	assert.Contains(t, out, `<vg-comp expr="c.Foo"></vg-comp>`)
	// and the static sibling is still compacted
	assert.Contains(t, out, `<p vg-html="vugu.HTML(`)
}

// Colon component references (pkg:Comp) must never be compacted; their static
// children are slot content and must stay live nodes.
func TestCompactNodeTreeColonComponent(t *testing.T) {

	in := `<html><head></head><body><div>
		<main:Widget><span>slot content</span></main:Widget>
		<p>static paragraph</p>
	</div></body></html>`

	out := compactTestRun(t, in)

	// the component element is untouched
	assert.Contains(t, out, `<main:Widget><span>slot content</span></main:Widget>`)
	// its slot child must not have been given a vg-html
	assert.NotContains(t, out, `<span vg-html=`)
	// the unrelated static sibling is still compacted
	assert.Contains(t, out, `<p vg-html="vugu.HTML(`)
}

// Component detection must use the original-cased tag name (OrigData); a
// component reference such as "main:Widget" is lowercased in Data by the
// parser but must still be recognized.
func TestIsComponentElement(t *testing.T) {

	assert.True(t, isComponentElement(&html.Node{Type: html.ElementNode, Data: "vg-comp"}))
	assert.True(t, isComponentElement(&html.Node{
		Type:     html.ElementNode,
		Data:     "main:widget",
		OrigData: "main:Widget",
	}))
	assert.True(t, isComponentElement(&html.Node{
		Type: html.ElementNode,
		Data: "main:widget",
	}))
	assert.False(t, isComponentElement(&html.Node{Type: html.ElementNode, Data: "div"}))
	assert.False(t, isComponentElement(&html.Node{Type: html.TextNode, Data: "main:Widget"}))
	assert.False(t, isComponentElement(&html.Node{Type: html.ElementNode, Data: "vg-template"}))
}

// Dynamic directive attributes (:attr, .prop, @event, vg-*) must prevent
// compaction of the element carrying them.
func TestIsStaticElDynamicAttrs(t *testing.T) {

	base := func(attrs ...html.Attribute) *html.Node {
		return &html.Node{Type: html.ElementNode, Data: "a", Attr: attrs}
	}

	assert.True(t, isStaticEl(base(html.Attribute{Key: "href", OrigKey: "href", Val: "/x"})))
	assert.False(t, isStaticEl(base(html.Attribute{Key: ":href", OrigKey: ":href", Val: "c.Url"})))
	assert.False(t, isStaticEl(base(html.Attribute{Key: ".value", OrigKey: ".value", Val: "c.V"})))
	assert.False(t, isStaticEl(base(html.Attribute{Key: "@click", OrigKey: "@click", Val: "c.Go"})))
	assert.False(t, isStaticEl(base(html.Attribute{Key: "vg-html", OrigKey: "vg-html", Val: "c.X"})))
	assert.False(t, isStaticEl(&html.Node{Type: html.ElementNode, Data: "vg-comp"}))
	assert.False(t, isStaticEl(&html.Node{Type: html.ElementNode, Data: "main:Widget"}))
}

// When an element is replaced by vg-html, the serialized subtree must not
// contain any dynamic directive attributes on descendants, or the dropped
// child generation could conflict with the static innerHTML output.
func TestCompactNodeTreeStripsResidualDirectives(t *testing.T) {

	// construct a subtree directly: a fully static <section> whose descendant
	// <a> (incorrectly) carries directive attributes. During serialization
	// those directives must be stripped rather than emitted into the HTML.
	doc, err := html.Parse(bytes.NewReader([]byte(
		`<html><head></head><body><section class="outer"><a :href="c.Url" @click="c.Go">go</a></section></body></html>`)))
	require.NoError(t, err)
	var section *html.Node
	var find func(*html.Node)
	find = func(nd *html.Node) {
		if nd.Type == html.ElementNode && nd.Data == "section" {
			section = nd
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	require.NotNil(t, section)

	require.NoError(t, compactElement(section))
	out := compactTestRender(t, section)

	assert.Contains(t, out, `class="outer"`)
	assert.NotContains(t, out, `:href=`)
	assert.NotContains(t, out, `@click=`)
	assert.Contains(t, out, `go`)
}

// head, script and style subtrees (and everything inside them) must never be
// modified.
func TestCompactNodeTreeFrozenElements(t *testing.T) {

	in := `<html><head>
		<title>Page <b>title</b></title>
		<style>.x { color: red; }</style>
	</head><body><div><p>static body</p></div>
	<script type="application/x-go">var x = 1</script>
	</body></html>`

	out := compactTestRun(t, in)

	assert.Contains(t, out, `&lt;b&gt;title&lt;/b&gt;`)
	assert.NotContains(t, out, `<title vg-html=`)
	assert.Contains(t, out, `<style>.x { color: red; }</style>`)
	assert.NotContains(t, out, `<style vg-html=`)
	assert.Contains(t, out, `var x = 1`)
	assert.NotContains(t, out, `<script vg-html=`)
	assert.Contains(t, out, `<div vg-html="vugu.HTML(`)
}

// The html and body tags themselves must never receive vg-html.
func TestCompactNodeTreeHtmlAndBodyUntouched(t *testing.T) {

	in := `<html><head></head><body><p>only static content here</p></body></html>`

	out := compactTestRun(t, in)

	assert.Contains(t, out, "<body>")
	assert.Contains(t, out, `<p vg-html="vugu.HTML(`)
	assert.NotContains(t, out, `<html vg-html=`)
	assert.NotContains(t, out, `<body vg-html=`)
}

// A fully static top-level element from a fragment parse (no html/body wrap)
// must also be compacted.
func TestCompactNodeTreeFragment(t *testing.T) {

	nodes, err := html.ParseFragment(bytes.NewReader([]byte(`<div><p>hello</p><span>world</span></div>`)), &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Div,
		Data:     "div",
	})
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.NoError(t, compactNodeTree(nodes[0]))

	out := compactTestRender(t, nodes[0])
	assert.Contains(t, out, `<div vg-html="vugu.HTML(`)
	assert.NotContains(t, out, "<p>hello</p>")
}

// Elements with no children are never given an empty vg-html.
func TestCompactNodeTreeSkipsEmpty(t *testing.T) {

	in := `<html><head></head><body><div><br/><img src="x.png"/><p>keep me</p></div></body></html>`
	out := compactTestRun(t, in)
	// the whole static div becomes a single vg-html, empty leaves do not get
	// their own (empty) vg-html attributes
	assert.NotContains(t, out, `<br vg-html=`)
	assert.NotContains(t, out, `<img vg-html=`)
	assert.Contains(t, out, `<div vg-html="vugu.HTML(`)
	assert.Contains(t, out, `x.png`)
	assert.Contains(t, out, `keep me`)
}

// An existing dynamic vg-html/vg-content expression must never be duplicated
// or replaced with a static value.
func TestCompactNodeTreePreservesDynamicVGHTML(t *testing.T) {

	in := `<html><head></head><body>
		<div vg-html="c.A">ignored <span>x</span></div>
		<div vg-content="c.B">ignored <span>y</span></div>
	</body></html>`

	out := compactTestRun(t, in)

	assert.Contains(t, out, `<div vg-html="c.A">ignored <span>x</span></div>`)
	assert.Contains(t, out, `<div vg-content="c.B">ignored <span>y</span></div>`)
	// children inside an innerHTML-directive element are never compacted
	assert.NotContains(t, out, `<span vg-html=`)
}
