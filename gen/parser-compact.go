package gen

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	// "github.com/vugu/vugu/internal/html"
	// "golang.org/x/net/html"
	"github.com/vugu/html"
)

// compactNodeTree operates on a Node tree in-place and finds elements with static
// contents and converts them to corresponding vg-html expressions with static output.
// Since vg-html ends up with a call to set innerHTML on an element in the DOM,
// it is much faster for large blocks of HTML than individual syncing DOM nodes.
// Any modern browser's native HTML parser is always going to be a lot faster than
// we can achieve calling back and forth from wasm for each element.
//
// The walk is strictly post-order. A subtree is classified as either fully
// static or not. Compaction only happens at a "barrier" node: an element that
// itself cannot be collapsed (dynamic attributes, a component, html/body,
// etc.). Such a node still compacts each of its independently static element
// children, which means static fragments nested inside dynamic content are
// optimized instead of the whole parent branch being left untouched.
func compactNodeTree(rootN *html.Node) error {

	var visit func(n *html.Node) (subtreeStatic bool, err error)
	visit = func(n *html.Node) (subtreeStatic bool, err error) {

		// non-element root containers (the DocumentNode returned by
		// html.Parse) only exist to hold their children; keep walking
		if n.Type != html.ElementNode {
			if n.FirstChild == nil {
				return true, nil
			}
			for n2 := n.FirstChild; n2 != nil; n2 = n2.NextSibling {
				if _, err := visit(n2); err != nil {
					return false, err
				}
			}
			return false, nil
		}

		// head, script and style must keep their contents verbatim, and
		// vg-* tags are compiler directives: nothing inside them is touched
		if isFrozenEl(n) {
			return false, nil
		}

		// an explicit dynamic vg-html/vg-content owns the element's contents
		// at runtime (SetInnerHTML replaces them), so its children are not
		// generated nodes that could be compacted and must be left alone
		if hasInnerHTMLDirective(n) {
			return false, nil
		}

		// a component element renders another component; its children are
		// slot content that must be compiled as live nodes. Neither the
		// element nor anything under it may receive vg-html.
		if isComponentElement(n) {
			return false, nil
		}

		// recurse first and track which children form fully static subtrees
		allChildrenStatic := true
		var staticChildren []*html.Node
		for n2 := n.FirstChild; n2 != nil; n2 = n2.NextSibling {
			childStatic, err := visit(n2)
			if err != nil {
				return false, err
			}
			if childStatic {
				staticChildren = append(staticChildren, n2)
			} else {
				allChildrenStatic = false
			}
		}

		// determine whether this whole element can be collapsed by a parent
		staticEl := isStaticEl(n) && !isRootContainerEl(n)

		// if the entire subtree is static, bubble it up unchanged so that an
		// ancestor can collapse it; unless this is the root of the walk (a
		// fragment's single top-level element), in which case compact it now
		if allChildrenStatic && staticEl {
			if n == rootN && n.FirstChild != nil {
				if err := compactElement(n); err != nil {
					return false, err
				}
				return false, nil
			}
			return true, nil
		}

		// otherwise n is a barrier: compact its static element children (each
		// of those subtrees is fully static, so collapsing it is safe), and
		// leave its non-static children to be handled by their own barriers
		for _, cn := range staticChildren {
			if err := compactElement(cn); err != nil {
				return false, err
			}
		}

		return false, nil
	}
	_, err := visit(rootN)

	return err
}

// isFrozenEl reports whether the element and its contents must be left exactly
// as-is by the compactor.
func isFrozenEl(n *html.Node) bool {
	return n.Type == html.ElementNode && (n.Data == "head" ||
		n.Data == "script" ||
		n.Data == "style" ||
		strings.HasPrefix(n.Data, "vg-"))
}

// hasInnerHTMLDirective reports whether the element carries a dynamic
// vg-html or vg-content expression.
func hasInnerHTMLDirective(n *html.Node) bool {
	return n.Type == html.ElementNode &&
		(attrWithKey(n, "vg-html") != nil || attrWithKey(n, "vg-content") != nil)
}

// isRootContainerEl reports whether the element is one of the document-level
// container tags that must keep their identity and never receive vg-html.
func isRootContainerEl(n *html.Node) bool {
	return n.Type == html.ElementNode && (n.Data == "html" || n.Data == "body")
}

// isComponentElement reports whether the element is a component reference rather
// than a plain DOM element. This must agree with the dispatch in
// visitDefaultByType: either a colon separates the package from the component
// name (case preserved via OrigData) or the element is an explicit vg-comp tag.
func isComponentElement(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if n.Data == "vg-comp" {
		return true
	}
	// OrigData keeps the original casing; fall back to Data
	name := n.OrigData
	if name == "" {
		name = n.Data
	}
	return strings.Contains(name, ":")
}

// compactElement replaces the children of the given fully static element with a
// single vg-html attribute containing the serialized static contents.
//
// The element is guaranteed to describe a fully static subtree, so its
// descendants never carry dynamic directives; the attributes are still cleaned
// defensively while serializing, to guarantee no stale vg-/:/./@-prefixed
// attributes are left on children whose generated DOM code is about to be
// discarded.
func compactElement(cn *html.Node) error {

	if cn.Type != html.ElementNode {
		return nil
	}

	var htmlBuf bytes.Buffer
	for cnChild := cn.FirstChild; cnChild != nil; cnChild = cnChild.NextSibling {
		stripDynamicAttrs(cnChild)
		if err := html.Render(&htmlBuf, cnChild); err != nil {
			return err
		}
	}

	// don't emit an empty vg-html (adds generated code with no benefit)
	if htmlBuf.Len() == 0 {
		return nil
	}

	// defend against a pre-existing vg-html/vg-content (such an element is
	// dynamic and should never reach here) so we never emit two innerHTML sets
	cn.Attr = removeAttrByKey(cn.Attr, "vg-html")
	cn.Attr = removeAttrByKey(cn.Attr, "vg-content")

	// add a vg-html with the static Go string expression of the contents
	// casted to a vugu.HTML
	cn.Attr = append(cn.Attr, html.Attribute{Key: "vg-html", Val: "vugu.HTML(" + htmlGoQuoteString(htmlBuf.String()) + ")"})

	// remove children, since vg-html supplants them
	cn.FirstChild = nil
	cn.LastChild = nil

	return nil
}

// stripDynamicAttrs walks a subtree and removes any attributes that carry
// vugu dynamic behavior. Nodes processed here are being serialized as static
// HTML (and will never go through normal code generation), so leaving a
// ":href", "@click", "vg-html", etc. behind would either emit the directive
// visibly in the static markup or silently drop its generated side effects.
func stripDynamicAttrs(n *html.Node) {
	if n == nil {
		return
	}
	if n.Type == html.ElementNode {
		filtered := n.Attr[:0]
		for _, attr := range n.Attr {
			key := attr.OrigKey
			if key == "" {
				key = attr.Key
			}
			// ":" binds an attribute, "." a property, "@" an event and
			// "vg-" (including vg-attr, vg-html, vg-js-*) anything else
			if strings.HasPrefix(key, "vg-") ||
				strings.HasPrefix(key, ":") ||
				strings.HasPrefix(key, ".") ||
				strings.HasPrefix(key, "@") {
				continue
			}
			filtered = append(filtered, attr)
		}
		n.Attr = filtered
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		stripDynamicAttrs(child)
	}
}

// removeAttrByKey returns attrs without entries matching the given key.
func removeAttrByKey(attrs []html.Attribute, key string) []html.Attribute {
	ret := attrs[:0]
	for _, attr := range attrs {
		if attr.Key == key {
			continue
		}
		ret = append(ret, attr)
	}
	return ret
}

func isStaticEl(n *html.Node) bool {

	if n.Type != html.ElementNode { // must be element
		return false
	}

	// component elements cannot be compacted (see isComponentElement)
	if isComponentElement(n) {
		return false
	}

	for _, attr := range n.Attr {
		key := attr.OrigKey
		if key == "" {
			key = attr.Key
		}
		if strings.HasPrefix(key, "vg-") { // vg- prefix means dynamic stuff
			return false
		}
		if strings.HasPrefix(key, ":") || // bound attribute
			strings.HasPrefix(key, ".") || // bound property
			strings.HasPrefix(key, "@") { // event handler
			return false
		}
		if len(key) == 0 { // avoid panic in this strange case
			continue
		}
		if !unicode.IsLetter(rune(key[0])) { // anything except a letter as an attr we assume to be dynamic
			return false
		}
	}

	// if it passes above, should be fine to compact
	return true

}

// htmlGoQuoteString is similar to printf'ing with %q but converts common things that require html escaping to
// backslashes instead for improved clarity
func htmlGoQuoteString(s string) string {

	var buf bytes.Buffer

	for _, c := range fmt.Sprintf("%q", s) {
		switch c {
		case '<', '>', '&':
			var qc string
			qc = fmt.Sprintf("\\x%X", uint8(c))
			buf.WriteString(qc)
		default:
			buf.WriteRune(c)
		}

	}

	return buf.String()
}
