package repomap

import (
	"errors"
	"slices"
)

// pageRank is power iteration matching networkx defaults (repomap-spec
// §3.5): alpha 0.85, max 100 iterations, uniform start, L1 convergence
// against nodeCount*1e-6, personalization and dangling vectors L1-normalized
// with absent nodes at 0, parallel edges pre-summed by the caller.
//
// nodes must be sorted (determinism); outWeights[i] maps neighbor index ->
// summed transition weight from node i.
func pageRank(nodes []string, outWeights []map[int]float64, personalization map[string]float64) (map[string]float64, error) {
	n := len(nodes)
	if n == 0 {
		return map[string]float64{}, nil
	}
	const (
		alpha   = 0.85
		maxIter = 100
		tol     = 1e-6
	)

	// Normalized personalization/dangling vector; nil means uniform.
	var pvec []float64
	if personalization != nil {
		pvec = make([]float64, n)
		sum := 0.0
		for i, name := range nodes {
			pvec[i] = personalization[name]
			sum += pvec[i]
		}
		if sum == 0 {
			return nil, errors.New("personalization sums to zero")
		}
		for i := range pvec {
			pvec[i] /= sum
		}
	}

	pAt := func(i int) float64 {
		if pvec == nil {
			return 1.0 / float64(n)
		}
		return pvec[i]
	}

	// Sorted adjacency: float accumulation order must be pinned (§6).
	type arc struct {
		j int
		w float64
	}
	adj := make([][]arc, n)
	totalOut := make([]float64, n)
	for i, out := range outWeights {
		keys := make([]int, 0, len(out))
		for j := range out {
			keys = append(keys, j)
		}
		slices.Sort(keys)
		for _, j := range keys {
			adj[i] = append(adj[i], arc{j, out[j]})
			totalOut[i] += out[j]
		}
	}

	x := make([]float64, n)
	for i := range x {
		x[i] = 1.0 / float64(n)
	}

	last := x
	for range maxIter {
		xNew := make([]float64, n)
		danglingSum := 0.0
		for i := range nodes {
			if totalOut[i] == 0 {
				danglingSum += alpha * last[i]
			}
		}
		for i := range nodes {
			xNew[i] = danglingSum*pAt(i) + (1-alpha)*pAt(i)
		}
		for i := range nodes {
			if totalOut[i] == 0 {
				continue
			}
			contrib := alpha * last[i] / totalOut[i]
			for _, a := range adj[i] {
				xNew[a.j] += contrib * a.w
			}
		}
		// L1 error against N*tol, per networkx.
		err := 0.0
		for i := range xNew {
			d := xNew[i] - last[i]
			if d < 0 {
				d = -d
			}
			err += d
		}
		last = xNew
		if err < float64(n)*tol {
			break
		}
	}
	// Non-convergence uses the last iterate rather than throwing (§3.5).

	out := make(map[string]float64, n)
	for i, name := range nodes {
		out[name] = last[i]
	}
	return out, nil
}
