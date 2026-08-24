package adf

import "strings"

// reconcile matches the edited markdown against the document it was rendered
// from, so that a block the author left alone comes back as the node it already
// was rather than as whatever markdown could say about it.
//
// The match is on the rendered lines, not on the parsed shape: a node is reused
// only when the markdown at that point is byte-for-byte what that node renders
// to. Matching the rendering rather than re-parsing and comparing is what makes
// this independent of how the parser would have divided those lines up — a
// heading holding a hard break is two blocks to markdown and one node to ADF,
// and it still comes back as the node.
func (p *parser) reconcile(ls []line, original []Node) ([]Node, error) {
	pieces := make([][]string, len(original))
	first := make(map[string][]int, len(original))
	for i := range original {
		p.scratch = AppendMarkdown(p.scratch[:0], NewDoc(original[i]), p.opt)
		if len(p.scratch) == 0 {
			continue
		}
		pieces[i] = strings.Split(string(p.scratch), "\n")
		first[pieces[i][0]] = append(first[pieces[i][0]], i)
	}

	var out []Node
	taken := 0
	// A match is tried before a blank line is skipped: a paragraph that opens
	// with a hard break renders with a blank first line, and skipping it would
	// look for that block one line too late.
	for i := 0; i < len(ls); {
		if at, lines, ok := match(ls, i, pieces, first[ls[i].text], taken); ok {
			out = append(out, unseen(original, pieces, taken, at)...)
			out = append(out, original[at].Clone())
			taken, i = at+1, i+lines
			continue
		}
		if ls[i].blank() {
			i++
			continue
		}
		node, next, err := p.block(ls, i, "doc")
		if err != nil {
			return nil, err
		}
		out = append(out, node)
		i = next
	}
	return append(out, unseen(original, pieces, taken, len(original))...), nil
}

// match reports which original node renders to exactly the lines starting at i,
// and how many lines that took. Reuse stays in document order — an edited block
// that happens to render like a block further down does not steal its node.
func match(ls []line, i int, pieces [][]string, candidates []int, taken int) (at, lines int, ok bool) {
	for _, c := range candidates {
		if c < taken || i+len(pieces[c]) > len(ls) {
			continue
		}
		if end := i + len(pieces[c]); end < len(ls) && !ls[end].blank() {
			continue
		}
		same := true
		for n, text := range pieces[c] {
			same = same && ls[i+n].text == text
		}
		if same {
			return c, len(pieces[c]), true
		}
	}
	return 0, 0, false
}

// unseen carries forward the nodes between two matches that render to nothing
// at all — an empty heading is the one the renderer really does erase. The
// author never saw them, so they cannot have deleted them; but if anything
// visible was deleted in the same span, the intent is gone and so are they.
func unseen(original []Node, pieces [][]string, from, to int) []Node {
	var out []Node
	for i := from; i < to; i++ {
		if len(pieces[i]) > 0 {
			return nil
		}
		out = append(out, original[i].Clone())
	}
	return out
}
