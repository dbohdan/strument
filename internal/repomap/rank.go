package repomap

import (
	"maps"
	"math"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// edgeKey identifies one (src, dst, ident) edge; weights per key are summed
// while retaining ident identity for rank distribution.
type edgeKey struct {
	src, dst, ident string
}

// getRankedTags ports get_ranked_tags: collect tags,
// build the weighted graph with the exact multipliers (sqrt computed once
// per referencer — declared deviation), run personalized PageRank,
// distribute rank across out-edges, and return the ranked MapItem list.
func (rm *RepoMap) getRankedTags(inv *invocation, chatFnames, otherFnames []string, mentionedFnames, mentionedIdents map[string]bool) []MapItem {
	defines := map[string]map[string]bool{}      // ident -> set(relFname)
	references := map[string][]string{}          // ident -> referencer relFnames, with repetition
	definitions := map[string]map[string][]Tag{} // relFname -> ident -> def tags

	personalization := map[string]float64{}

	chatSet := map[string]bool{}
	for _, f := range chatFnames {
		chatSet[f] = true
	}
	fnameSet := map[string]bool{}
	for _, f := range chatFnames {
		fnameSet[f] = true
	}
	for _, f := range otherFnames {
		fnameSet[f] = true
	}
	fnames := slices.Sorted(maps.Keys(fnameSet))

	chatRelFnames := map[string]bool{}

	personalize := 100.0 / float64(len(fnames))

	for _, fname := range fnames {
		st, err := os.Stat(fname)
		if err != nil || st.IsDir() {
			if !rm.warnedFiles[fname] {
				rm.warnf("Repo-map can't include %s", fname)
				rm.warnedFiles[fname] = true
			}
			continue
		}

		relFname := rm.relFname(fname)
		currentPers := 0.0

		if chatSet[fname] {
			currentPers += personalize
			chatRelFnames[relFname] = true
		}
		if mentionedFnames[relFname] {
			// max, not add: no double counting for chat+mentioned.
			currentPers = math.Max(currentPers, personalize)
		}

		// Path components (each part, basename with and without extension)
		// against mentioned identifiers: add once on any match.
		if len(mentionedIdents) > 0 {
			components := map[string]bool{}
			for part := range strings.SplitSeq(relFname, "/") {
				components[part] = true
			}
			base := path.Base(relFname)
			components[base] = true
			components[strings.TrimSuffix(base, path.Ext(base))] = true
			for c := range components {
				if mentionedIdents[c] {
					currentPers += personalize
					break
				}
			}
		}

		if currentPers > 0 {
			personalization[relFname] = currentPers
		}

		for _, tag := range inv.tags(rm, fname, relFname) {
			switch tag.Kind {
			case Def:
				if defines[tag.Name] == nil {
					defines[tag.Name] = map[string]bool{}
				}
				defines[tag.Name][relFname] = true
				if definitions[relFname] == nil {
					definitions[relFname] = map[string][]Tag{}
				}
				definitions[relFname][tag.Name] = append(definitions[relFname][tag.Name], tag)
			case Ref:
				references[tag.Name] = append(references[tag.Name], relFname)
			}
		}
	}

	// Degenerate fallback: no refs anywhere -> treat definers as referencers.
	if len(references) == 0 {
		references = map[string][]string{}
		for name, definers := range defines {
			references[name] = slices.Sorted(maps.Keys(definers))
		}
	}

	// idents defined AND referenced.
	var idents []string
	for name := range defines {
		if _, ok := references[name]; ok {
			idents = append(idents, name)
		}
	}
	slices.Sort(idents)

	// Build the multigraph, retaining per-(src,dst,ident) weights.
	edgeWeights := map[edgeKey]float64{}
	var edgeOrder []edgeKey // first-seen order for deterministic node set
	addEdge := func(src, dst, ident string, w float64) {
		k := edgeKey{src, dst, ident}
		if _, ok := edgeWeights[k]; !ok {
			edgeOrder = append(edgeOrder, k)
		}
		edgeWeights[k] += w
	}

	// Self-edges for defined-but-unreferenced idents.
	for _, name := range slices.Sorted(maps.Keys(defines)) {
		if _, ok := references[name]; ok {
			continue
		}
		for _, definer := range slices.Sorted(maps.Keys(defines[name])) {
			addEdge(definer, definer, name, 0.1)
		}
	}

	for _, ident := range idents {
		definers := slices.Sorted(maps.Keys(defines[ident]))

		mul := 1.0
		isSnake := strings.Contains(ident, "_") && hasAlpha(ident)
		isKebab := strings.Contains(ident, "-") && hasAlpha(ident)
		isCamel := hasUpper(ident) && hasLower(ident)
		if mentionedIdents[ident] {
			mul *= 10
		}
		if (isSnake || isKebab || isCamel) && len(ident) >= 8 {
			mul *= 10
		}
		if strings.HasPrefix(ident, "_") {
			mul *= 0.1
		}
		if len(defines[ident]) > 5 {
			mul *= 0.1
		}

		counts := map[string]int{}
		for _, r := range references[ident] {
			counts[r]++
		}
		for _, referencer := range slices.Sorted(maps.Keys(counts)) {
			// sqrt once per referencer (declared deviation from the
			// upstream compounding sqrt inside the definer loop).
			w := math.Sqrt(float64(counts[referencer]))
			useMul := mul
			if chatRelFnames[referencer] {
				useMul *= 50
			}
			for _, definer := range definers {
				addEdge(referencer, definer, ident, useMul*w)
			}
		}
	}

	// Node set: files touched by an edge, sorted (a file becomes a
	// node only when an edge touches it).
	nodeSet := map[string]bool{}
	for _, k := range edgeOrder {
		nodeSet[k.src] = true
		nodeSet[k.dst] = true
	}
	nodes := slices.Sorted(maps.Keys(nodeSet))
	nodeIdx := map[string]int{}
	for i, n := range nodes {
		nodeIdx[n] = i
	}

	// Transition weights: parallel edges summed per (src, dst).
	outWeights := make([]map[int]float64, len(nodes))
	for i := range outWeights {
		outWeights[i] = map[int]float64{}
	}
	// Deterministic accumulation: sorted edge keys.
	sortedKeys := append([]edgeKey(nil), edgeOrder...)
	sort.Slice(sortedKeys, func(a, b int) bool {
		x, y := sortedKeys[a], sortedKeys[b]
		if x.src != y.src {
			return x.src < y.src
		}
		if x.dst != y.dst {
			return x.dst < y.dst
		}
		return x.ident < y.ident
	})
	for _, k := range sortedKeys {
		outWeights[nodeIdx[k.src]][nodeIdx[k.dst]] += edgeWeights[k]
	}

	// PageRank with the failure taxonomy.
	var ranked map[string]float64
	if len(nodes) == 0 {
		ranked = map[string]float64{}
	} else {
		pers := personalization
		var err error
		if len(pers) == 0 {
			ranked, err = pageRank(nodes, outWeights, nil)
		} else {
			ranked, err = pageRank(nodes, outWeights, pers)
			if err != nil {
				ranked, err = pageRank(nodes, outWeights, nil)
			}
		}
		if err != nil {
			return nil
		}
	}

	// Distribute rank across out-edges by weight fraction, per ident.
	type defKey struct{ fname, ident string }
	rankedDefinitions := map[defKey]float64{}
	for _, src := range nodes {
		srcRank := ranked[src]
		total := 0.0
		var outKeys []edgeKey
		for _, k := range sortedKeys {
			if k.src == src {
				outKeys = append(outKeys, k)
				total += edgeWeights[k]
			}
		}
		if total == 0 {
			continue
		}
		for _, k := range outKeys {
			rankedDefinitions[defKey{k.dst, k.ident}] += srcRank * edgeWeights[k] / total
		}
	}

	// Sort by (rank desc, (fname, ident) desc) — the tuple tiebreak is
	// reverse, matching sorted(..., reverse=True, key=(rank, key)).
	type rankedDef struct {
		fname, ident string
		rank         float64
	}
	defs := make([]rankedDef, 0, len(rankedDefinitions))
	for k, r := range rankedDefinitions {
		defs = append(defs, rankedDef{k.fname, k.ident, r})
	}
	sort.Slice(defs, func(a, b int) bool {
		x, y := defs[a], defs[b]
		if x.rank != y.rank {
			return x.rank > y.rank
		}
		if x.fname != y.fname {
			return x.fname > y.fname
		}
		return x.ident > y.ident
	})

	var rankedTags []MapItem
	includedFnames := map[string]bool{}
	for _, d := range defs {
		if chatRelFnames[d.fname] {
			continue
		}
		tags := append([]Tag(nil), definitions[d.fname][d.ident]...)
		slices.SortFunc(tags, func(a, b Tag) int {
			if lessTag(a, b) {
				return -1
			}
			if lessTag(b, a) {
				return 1
			}
			return 0
		})
		for _, t := range tags {
			tt := t
			rankedTags = append(rankedTags, MapItem{RelFname: t.RelFname, Tag: &tt})
			includedFnames[t.RelFname] = true
		}
	}

	// Bare-node pass over all graph nodes (chat files included; they are
	// skipped later in toTree but still count toward truncation):
	// (rank desc, node desc).
	relOtherWithoutTags := map[string]bool{}
	for _, f := range otherFnames {
		relOtherWithoutTags[rm.relFname(f)] = true
	}
	topRank := append([]string(nil), nodes...)
	sort.Slice(topRank, func(a, b int) bool {
		ra, rb := ranked[topRank[a]], ranked[topRank[b]]
		if ra != rb {
			return ra > rb
		}
		return topRank[a] > topRank[b]
	})
	for _, node := range topRank {
		delete(relOtherWithoutTags, node)
		if !includedFnames[node] {
			rankedTags = append(rankedTags, MapItem{RelFname: node})
		}
	}
	for _, fname := range slices.Sorted(maps.Keys(relOtherWithoutTags)) {
		rankedTags = append(rankedTags, MapItem{RelFname: fname})
	}

	return rankedTags
}

func hasAlpha(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func hasLower(s string) bool {
	for _, r := range s {
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}
