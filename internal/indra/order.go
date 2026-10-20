package indra

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"

	ort "github.com/yalue/onnxruntime_go"
)

const nFeats = 16

// Block classes the order model's class head predicts per line.
const (
	ClsText = iota
	ClsSectionHeader
	ClsPageHeader
	ClsPageFooter
	ClsListGroup
	ClsTable
	ClsCaption
	ClsFootnote
	ClsTableOfContents
	ClsForm
	nClasses
)

func isGridClass(c int) bool { return c == ClsTable || c == ClsForm || c == ClsTableOfContents }

func displayLen(t string) int {
	n := 0
	for _, r := range t {
		switch {
		case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
			r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFF60,
			r >= 0xFFE0 && r <= 0xFFE6, r >= 0x20000 && r <= 0x3FFFD:
			n += 2
		default:
			n++
		}
	}
	return n
}

// lineFeatures builds the order model's [N, 16] input: ten geometry features
// and six text features per line.
func lineFeatures(lines []Line, pw, ph float64) []float32 {
	n := len(lines)
	hs := make([]float64, n)
	for i, l := range lines {
		hs[i] = math.Max(l.Box.Y1-l.Box.Y0, 1e-3)
	}
	sorted := slices.Clone(hs)
	slices.Sort(sorted)
	medH := median(sorted)
	out := make([]float32, n*nFeats)
	for i, l := range lines {
		b := l.Box
		w := math.Max(b.X1-b.X0, 1e-3)
		h := hs[i]
		f := out[i*nFeats : (i+1)*nFeats]
		f[0], f[1], f[2], f[3] = float32(b.X0/pw), float32(b.Y0/ph), float32(b.X1/pw), float32(b.Y1/ph)
		f[4], f[5] = float32((b.X0+b.X1)/(2*pw)), float32((b.Y0+b.Y1)/(2*ph))
		f[6] = float32(w / pw)
		f[7] = float32(math.Log(h / medH))
		f[8] = float32(math.Log(w/h) / 4)
		f[9] = float32(medH / ph * 20)
		t := []rune(strings.TrimSpace(l.Text))
		if len(t) == 0 {
			continue
		}
		f[10] = float32(math.Log1p(float64(displayLen(string(t)))) / 5)
		digits := 0
		for _, r := range t {
			if unicode.IsDigit(r) {
				digits++
			}
		}
		f[11] = float32(digits) / float32(len(t))
		head := string(t[:min(3, len(t))])
		head = strings.TrimRight(head, ".)")
		isNum := head != "" && strings.IndexFunc(head, func(r rune) bool { return !unicode.IsDigit(r) }) < 0
		if strings.ContainsRune("•-–▪◦*·→", t[0]) || (isNum && len(t) > 3) {
			f[12] = 1
		}
		if t[len(t)-1] == '-' {
			f[13] = 1
		}
		if strings.ContainsRune(".!?:;。！？：；．", t[len(t)-1]) {
			f[14] = 1
		}
		if unicode.IsUpper(t[0]) {
			f[15] = 1
		}
	}
	return out
}

// Layout is the result of the order model for one page: the reading order
// of the lines, per-line block class logits [N][nClasses] and the pairwise
// "same block" logits [N][N].
type Layout struct {
	N     int
	Order []int
	Cls   []float32
	Same  []float32
}

// ReadingOrder groups the lines into blocks and orders blocks and lines.
func (m *Model) ReadingOrder(lines []Line, pw, ph int) (*Layout, error) {
	n := len(lines)
	if n < 2 {
		return &Layout{N: n, Order: make([]int, n)}, nil
	}
	feats := lineFeatures(lines, float64(pw), float64(ph))
	ft, err := ort.NewTensor(ort.NewShape(1, int64(n), nFeats), feats)
	if err != nil {
		return nil, err
	}
	defer ft.Destroy()
	mask := make([]bool, n)
	for i := range mask {
		mask[i] = true
	}
	mt, err := ort.NewTensor(ort.NewShape(1, int64(n)), mask)
	if err != nil {
		return nil, err
	}
	defer mt.Destroy()
	outs := []ort.Value{nil, nil, nil}
	if err := m.order.Run([]ort.Value{ft, mt}, outs); err != nil {
		return nil, fmt.Errorf("indra order: %w", err)
	}
	defer func() {
		for _, o := range outs {
			o.Destroy()
		}
	}()
	get := func(i int) ([]float32, error) {
		t, ok := outs[i].(*ort.Tensor[float32])
		if !ok {
			return nil, fmt.Errorf("indra order: output %d has an unexpected type", i)
		}
		return t.GetData(), nil
	}
	a, err := get(0)
	if err != nil {
		return nil, err
	}
	cls, err := get(1)
	if err != nil {
		return nil, err
	}
	same, err := get(2)
	if err != nil {
		return nil, err
	}
	boxes := make([][4]float64, n)
	for i, l := range lines {
		boxes[i] = [4]float64{l.Box.X0, l.Box.Y0, l.Box.X1, l.Box.Y1}
	}
	return &Layout{N: n, Order: decodeBlocksOrder(a, same, cls, boxes), Cls: slices.Clone(cls), Same: slices.Clone(same)}, nil
}

// decodeOrder turns a pairwise logit matrix (a[i*n+j] > 0: i before j) into an
// order: row-sum ranking, then insertion moves that raise the summed
// log-probability of the pairwise relations.
func decodeOrder(a []float64, n int) []int {
	if n <= 1 {
		return make([]int, n)
	}
	lp := make([]float64, n*n)
	for i, v := range a {
		lp[i] = -(math.Max(0, -v) + math.Log1p(math.Exp(-math.Abs(v)))) // -logaddexp(0, -v)
	}
	score := make([]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			score[i] += -lp[i*n+j] + lp[j*n+i]
		}
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool { return score[order[x]] < score[order[y]] })
	for sweep := 0; sweep < 4; sweep++ {
		improved := false
		for _, x := range slices.Clone(order) {
			rest := make([]int, 0, n-1)
			cur := 0
			for p, v := range order {
				if v == x {
					cur = p
				} else {
					rest = append(rest, v)
				}
			}
			m := len(rest)
			// gains[k]: x inserted before rest[k] = sum(lp[rest[:k], x]) + sum(lp[x, rest[k:]])
			gains := make([]float64, m+1)
			suffix := 0.0
			for k := m; k >= 0; k-- {
				gains[k] = suffix
				if k > 0 {
					suffix += lp[x*n+rest[k-1]]
				}
			}
			prefix := 0.0
			for k := 0; k <= m; k++ {
				gains[k] += prefix
				if k < m {
					prefix += lp[rest[k]*n+x]
				}
			}
			best := 0
			for k := 1; k <= m; k++ {
				if gains[k] > gains[best] {
					best = k
				}
			}
			if gains[best] > gains[cur]+1e-6 {
				order = slices.Insert(rest, best, x)
				improved = true
			}
		}
		if !improved {
			break
		}
	}
	return order
}

// cluster is average-linkage clustering of pairwise "same group" logits: merge
// the two groups with the highest mean logit between them while it is positive.
func cluster(logits []float32, n int) [][]int {
	if n == 0 {
		return nil
	}
	ninf := math.Inf(-1)
	S := make([]float64, n*n)
	for i := range S {
		S[i] = float64(logits[i])
	}
	for i := 0; i < n; i++ {
		S[i*n+i] = 0
	}
	size := make([]float64, n)
	alive := make([]bool, n)
	members := make([][]int, n)
	for i := range size {
		size[i], alive[i], members[i] = 1, true, []int{i}
	}
	M := slices.Clone(S)
	for i := 0; i < n; i++ {
		M[i*n+i] = ninf
	}
	best := make([]int, n)
	bestV := make([]float64, n)
	rowArgmax := func(i int) {
		bj, bv := 0, M[i*n]
		for j := 1; j < n; j++ {
			if M[i*n+j] > bv {
				bj, bv = j, M[i*n+j]
			}
		}
		best[i], bestV[i] = bj, bv
	}
	for i := 0; i < n; i++ {
		rowArgmax(i)
	}
	row := make([]float64, n)
	for step := 0; step < n-1; step++ {
		a := 0
		for i := 1; i < n; i++ {
			if bestV[i] > bestV[a] {
				a = i
			}
		}
		b := best[a]
		if bestV[a] <= 0 {
			break
		}
		a, b = min(a, b), max(a, b)
		for j := 0; j < n; j++ {
			S[a*n+j] += S[b*n+j]
		}
		for i := 0; i < n; i++ {
			S[i*n+a] += S[i*n+b]
		}
		S[a*n+a] = 0
		size[a] += size[b]
		alive[b] = false
		members[a] = append(members[a], members[b]...)
		for j := 0; j < n; j++ {
			if !alive[j] || j == a {
				row[j] = ninf
			} else {
				row[j] = S[a*n+j] / (size[a] * size[j])
			}
		}
		for j := 0; j < n; j++ {
			M[a*n+j], M[j*n+a] = row[j], row[j]
			M[b*n+j], M[j*n+b] = ninf, ninf
		}
		bestV[b] = ninf
		rowArgmax(a)
		for j := 0; j < n; j++ {
			if alive[j] && j != a && (best[j] == a || best[j] == b) {
				rowArgmax(j)
			}
		}
		for j := 0; j < n; j++ {
			if alive[j] && j != a && row[j] > bestV[j] {
				best[j], bestV[j] = a, row[j]
			}
		}
	}
	var out [][]int
	for i := 0; i < n; i++ {
		if alive[i] {
			m := slices.Clone(members[i])
			slices.Sort(m)
			out = append(out, m)
		}
	}
	return out
}

// orderTopDown: rows top to bottom (centres within half a line height share a
// row), left to right. Returns indices into boxes.
func orderTopDown(boxes [][4]float64) []int {
	n := len(boxes)
	if n == 0 {
		return nil
	}
	// float32 throughout, like the reference: a row boundary decides ties.
	hs := make([]float64, n)
	for i, b := range boxes {
		hs[i] = float64(float32(b[3]) - float32(b[1]))
	}
	slices.Sort(hs)
	step := float32(math.Max(float64(float32(median(hs))*0.5), 1e-3))
	row := func(i int) int {
		cy := (float32(boxes[i][1]) + float32(boxes[i][3])) / 2
		return roundEven(float64(cy / step))
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(x, y int) bool {
		rx, ry := row(idx[x]), row(idx[y])
		if rx != ry {
			return rx < ry
		}
		return boxes[idx[x]][0] < boxes[idx[y]][0]
	})
	return idx
}

// bestCut finds the widest whitespace gap along an axis (1 = rows, 0 = columns).
func bestCut(b [][4]float64, axis int) (gap float64, k int, o []int) {
	lo, hi := 0, 2
	if axis == 1 {
		lo, hi = 1, 3
	}
	o = make([]int, len(b))
	for i := range o {
		o[i] = i
	}
	sort.SliceStable(o, func(x, y int) bool { return b[o[x]][lo] < b[o[y]][lo] })
	end := b[o[0]][hi]
	k = -1
	for i := 1; i < len(o); i++ {
		if g := b[o[i]][lo] - end; g > gap {
			gap, k = g, i
		}
		end = math.Max(end, b[o[i]][hi])
	}
	return
}

// orderXYCut is a recursive XY-cut: split at the widest whitespace gap on
// either axis, upper or left part first; without a gap, top-down order.
func orderXYCut(boxes [][4]float64) []int {
	ids := make([]int, len(boxes))
	for i := range ids {
		ids[i] = i
	}
	var rec func(ids []int) []int
	rec = func(ids []int) []int {
		if len(ids) <= 1 {
			return slices.Clone(ids)
		}
		b := make([][4]float64, len(ids))
		for i, id := range ids {
			b[i] = boxes[id]
		}
		gy, ky, oy := bestCut(b, 1)
		gx, kx, ox := bestCut(b, 0)
		if ky < 0 && kx < 0 {
			var out []int
			for _, i := range orderTopDown(b) {
				out = append(out, ids[i])
			}
			return out
		}
		k, o := kx, ox
		if gy >= gx {
			k, o = ky, oy
		}
		pick := func(sel []int) []int {
			s := make([]int, len(sel))
			for i, v := range sel {
				s[i] = ids[v]
			}
			return s
		}
		return append(rec(pick(o[:k])), rec(pick(o[k:]))...)
	}
	return rec(ids)
}

func decodeBlocksOrder(a, same, cls []float32, boxes [][4]float64) []int {
	n := len(boxes)
	groups := cluster(same, n)
	inner := make([][]int, len(groups))
	for gi, g := range groups {
		bx := make([][4]float64, len(g))
		for i, id := range g {
			bx[i] = boxes[id]
		}
		// The block's class: the largest summed class logit over its lines.
		c := argmaxSum(cls, g)
		var local []int
		if isGridClass(c) {
			local = orderTopDown(bx)
		} else {
			local = orderXYCut(bx)
		}
		inner[gi] = make([]int, len(local))
		for i, k := range local {
			inner[gi][i] = g[k]
		}
	}
	// Mean order logit between blocks p < q, antisymmetric.
	m := len(groups)
	ab := make([]float64, m*m)
	for p := 0; p < m; p++ {
		for q := p + 1; q < m; q++ {
			s := 0.0
			for _, i := range groups[p] {
				for _, j := range groups[q] {
					s += float64(a[i*n+j])
				}
			}
			v := s / float64(len(groups[p])*len(groups[q]))
			ab[p*m+q], ab[q*m+p] = v, -v
		}
	}
	var out []int
	for _, k := range decodeOrder(ab, m) {
		out = append(out, inner[k]...)
	}
	return out
}

// argmaxSum is the class with the largest logit summed over the given lines.
func argmaxSum(cls []float32, lines []int) int {
	sum := make([]float64, nClasses)
	for _, id := range lines {
		for c := 0; c < nClasses; c++ {
			sum[c] += float64(cls[id*nClasses+c])
		}
	}
	c := 0
	for i := 1; i < nClasses; i++ {
		if sum[i] > sum[c] {
			c = i
		}
	}
	return c
}
