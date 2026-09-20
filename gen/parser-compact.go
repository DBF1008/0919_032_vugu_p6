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
// The walk is strictly post-order and each element subtree is classified as either
// fully static or not.  Compaction happens at "barrier" nodes: elements that cannot
// themselves be collapsed (dynamic attributes, component references, html/body
// containers, frozen tags, etc.).  A barrier still compacts each of its
// independently static element children, so static fragments nested inside
// dynamic content are optimized instead of the whole branch being left as-is.
// If the root node itself is a fully static element (the common case of a
// single-root-element component fragment), it is compacted as well.
func compactNodeTree(rootN *html.Node) error {

	var visit func(n *html.Node) (subtreeStatic bool, err error)
	visit = func(n *html.Node) (subtreeStatic bool, err error) {

		// the document node is just a container, recurse but never compact it
		if n.Type == html.DocumentNode {
			for childN := n.FirstChild; childN != nil; childN = childN.NextSibling {
				if _, err := visit(childN); err != nil {
					return false, err
				}
			}
			return false, nil
		}

		// other non-element nodes (text, comments, doctype) are inherently static
		// and get serialized as part of their parent element's contents
		if n.Type != html.ElementNode {
			return true, nil
		}

		// head, script and style must keep their contents verbatim, and vg-* tags
		// (vg-comp, vg-template, vg-slot, ...) are compiler directives:
		// neither these elements nor anything inside them is touched
		if isFrozenEl(n) {
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

		// if the entire subtree is static and this element can itself be collapsed,
		// bubble it up unchanged; an ancestor (or the root handling below) will
		// perform the actual collapse
		if allChildrenStatic && isStaticEl(n) && !isRootContainerEl(n) {
			return true, nil
		}

		// otherwise n is a barrier: compact each of its static element children
		// (each of those subtrees is fully static, so collapsing it is safe) and
		// leave the non-static children, which were already handled by their own
		// barriers during the recursion above
		for _, cn := range staticChildren {
			if err := compactElement(cn); err != nil {
				return false, err
			}
		}

		return false, nil
	}

	rootStatic, err := visit(rootN)
	if err != nil {
		return err
	}

	// a fully static root element (single-root-element component fragment)
	// has no ancestor to compact it, so do it here
	if rootStatic && rootN.Type == html.ElementNode {
		if err := compactElement(rootN); err != nil {
			return err
		}
	}

	return nil
}

// isFrozenEl reports whether the element and its contents must be left exactly
// as-is by the compactor.
func isFrozenEl(n *html.Node) bool {
	return n.Type == html.ElementNode && (n.Data == "head" ||
		n.Data == "script" ||
		n.Data == "style" ||
		strings.HasPrefix(n.Data, "vg-"))
}

// isRootContainerEl reports whether the element is one of the document-level
// container tags that must keep their identity and never receive a vg-html
// attribute themselves (their static children are still compacted).
func isRootContainerEl(n *html.Node) bool {
	return n.Type == html.ElementNode && (n.Data == "html" || n.Data == "body")
}

// isComponentElement reports whether the element is a component reference rather
// than a plain DOM element.  This must agree with the dispatch in
// visitDefaultByType (parser-go.go): either the element is an explicit vg-comp
// tag, or a colon separates the package from the component name.  The original
// casing (OrigData) is used when available, consistent with how
// visitNodeComponentElement resolves the component type.
func isComponentElement(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if n.Data == "vg-comp" {
		return true
	}
	name := n.OrigData
	if name == "" {
		name = n.Data
	}
	return strings.Contains(name, ":")
}

// compactElement replaces the children of the given fully static element with a
// single vg-html attribute containing the serialized static contents.
func compactElement(cn *html.Node) error {

	if cn.Type != html.ElementNode {
		return nil
	}

	var htmlBuf bytes.Buffer
	// walk each immediate child of cn and render it into htmlBuf
	for cnChild := cn.FirstChild; cnChild != nil; cnChild = cnChild.NextSibling {
		// clean any residual dynamic attributes before serializing; a fully
		// static subtree should not carry any, but if one slips through (or a
		// previous pass left a vg-html behind) it must not end up baked into
		// the static markup, where it would conflict with generated code
		stripDynamicAttrs(cnChild)
		err := html.Render(&htmlBuf, cnChild)
		if err != nil {
			return err
		}
	}

	// don't emit an empty vg-html, it adds generated code with no benefit
	if htmlBuf.Len() == 0 {
		return nil
	}

	// remove any pre-existing vg-html/vg-content so we never emit two
	// conflicting innerHTML expressions on the same element
	cn.Attr = removeAttrByKey(cn.Attr, "vg-html")
	cn.Attr = removeAttrByKey(cn.Attr, "vg-content")

	// add a vg-html with the static Go string expression of the contents casted to a vugu.HTML
	cn.Attr = append(cn.Attr, html.Attribute{Key: "vg-html", Val: "vugu.HTML(" + htmlGoQuoteString(htmlBuf.String()) + ")"})

	// remove children, since vg-html supplants them
	cn.FirstChild = nil
	cn.LastChild = nil

	return nil
}

// stripDynamicAttrs walks a subtree and removes any attributes that carry vugu
// dynamic behavior ("vg-*", ":bound", ".prop", "@event").  Nodes processed here
// are being serialized as static HTML and will never go through normal code
// generation, so leaving such an attribute behind would either emit the
// directive visibly in the static markup or conflict with the generated code
// for the element.
func stripDynamicAttrs(n *html.Node) {
	if n == nil {
		return
	}
	if n.Type == html.ElementNode {
		filtered := n.Attr[:0]
		for _, attr := range n.Attr {
			if isDynamicAttr(attr) {
				continue
			}
			filtered = append(filtered, attr)
		}
		n.Attr = filtered
	}
	for childN := n.FirstChild; childN != nil; childN = childN.NextSibling {
		stripDynamicAttrs(childN)
	}
}

// isDynamicAttr reports whether an attribute carries vugu dynamic behavior:
// "vg-*" directives, ":bound" attributes, ".prop" properties and "@event" handlers.
func isDynamicAttr(attr html.Attribute) bool {
	key := attr.OrigKey
	if key == "" {
		key = attr.Key
	}
	if strings.HasPrefix(key, ":") ||
		strings.HasPrefix(key, ".") ||
		strings.HasPrefix(key, "@") {
		return true
	}
	// vg- directives are matched case-insensitively, consistent with how the
	// parser looks them up via the lower-cased attribute Key
	return strings.HasPrefix(strings.ToLower(key), "vg-")
}

// removeAttrByKey returns attrs without the entries matching the given key.
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

	// component elements cannot be compacted, whether referenced with a
	// colon-separated tag (pkg:Comp) or directly with a vg-comp tag
	if isComponentElement(n) {
		return false
	}

	for _, attr := range n.Attr {
		if isDynamicAttr(attr) { // vg-*, :, . and @ attributes mean dynamic stuff
			return false
		}
		key := attr.OrigKey
		if key == "" {
			key = attr.Key
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
