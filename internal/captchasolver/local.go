// Package captchasolver includes a local, no-network CXCaptcha solver.
package captchasolver

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sort"
)

const (
	localBorder     = 25
	localSeparation = 20
	localLargeScore = 1e9
	localFusedFloor = -1e8
)

type LocalSolver struct{}

func NewLocal() *LocalSolver { return &LocalSolver{} }

func (s *LocalSolver) Solve(_ context.Context, background, cutout []byte) (int, error) {
	offsets, err := RankOffsets(background, cutout, 1)
	if err != nil || len(offsets) == 0 {
		return 0, errors.New("local captcha detection found no offset")
	}
	return offsets[0], nil
}

func RankOffsets(background, cutout []byte, limit int) ([]int, error) {
	if limit <= 0 {
		limit = 5
	}
	shade, _, err := image.Decode(bytes.NewReader(background))
	if err != nil {
		return nil, errors.New("captcha background is not a valid image")
	}
	piece, _, err := image.Decode(bytes.NewReader(cutout))
	if err != nil {
		return nil, errors.New("captcha cutout is not a valid image")
	}
	model, err := newLocalModel(shade, piece)
	if err != nil {
		return nil, err
	}
	return model.rank(localBorder, localSeparation, limit), nil
}

type localPoint struct{ X, Y int }

type localModel struct {
	shade, piece             image.Image
	mask                     [][]bool
	boundary                 []localPoint
	left, right, top, bottom int
	lum, gradX, gradY        [][]float64
	maxOffset                int
}

func newLocalModel(shade, piece image.Image) (*localModel, error) {
	bounds, pBounds := shade.Bounds(), piece.Bounds()
	width, height := pBounds.Dx(), pBounds.Dy()
	if width <= 0 || height <= 0 || bounds.Dx() < width {
		return nil, errors.New("captcha image dimensions are invalid")
	}
	m := &localModel{shade: shade, piece: piece, maxOffset: bounds.Dx() - width}
	m.mask = make([][]bool, height)
	m.left, m.right, m.top, m.bottom = width, -1, height, -1
	for y := 0; y < height; y++ {
		m.mask[y] = make([]bool, width)
		for x := 0; x < width; x++ {
			_, _, _, alpha := piece.At(pBounds.Min.X+x, pBounds.Min.Y+y).RGBA()
			if alpha > 128<<8 {
				m.mask[y][x] = true
				if x < m.left {
					m.left = x
				}
				if x > m.right {
					m.right = x
				}
				if y < m.top {
					m.top = y
				}
				if y > m.bottom {
					m.bottom = y
				}
			}
		}
	}
	if m.right < m.left || m.bottom < m.top {
		return nil, errors.New("captcha cutout has no visible pixels")
	}
	for y := 1; y < height-1; y++ {
		for x := 1; x < width-1; x++ {
			if !m.mask[y][x] {
				continue
			}
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {2, 0}, {-2, 0}, {0, 2}, {0, -2}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= width || ny >= height || !m.mask[ny][nx] {
					m.boundary = append(m.boundary, localPoint{x, y})
					break
				}
			}
		}
	}
	m.lum, m.gradX, m.gradY = make([][]float64, bounds.Dy()), make([][]float64, bounds.Dy()), make([][]float64, bounds.Dy())
	for y := 0; y < bounds.Dy(); y++ {
		m.lum[y], m.gradX[y], m.gradY[y] = make([]float64, bounds.Dx()), make([]float64, bounds.Dx()), make([]float64, bounds.Dx())
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, _ := shade.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			m.lum[y][x] = 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
		}
	}
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			if x > 0 && x < bounds.Dx()-1 {
				m.gradX[y][x] = (m.lum[y][x+1] - m.lum[y][x-1]) / 2
			}
			if y > 0 && y < bounds.Dy()-1 {
				m.gradY[y][x] = (m.lum[y+1][x] - m.lum[y-1][x]) / 2
			}
		}
	}
	return m, nil
}

func (m *localModel) rank(border, separation, limit int) []int {
	count := m.maxOffset + 1
	if count <= 0 {
		return nil
	}
	edge, ncc, dark, sad := make([]float64, count), make([]float64, count), make([]float64, count), make([]float64, count)
	for offset := 0; offset < count; offset++ {
		edge[offset], ncc[offset], dark[offset], sad[offset] = m.edgeScore(offset), m.nccScore(offset), m.darkScore(offset), m.sadScore(offset)
	}
	hi := m.maxOffset - border
	if hi <= border {
		return nil
	}
	ne := normalizeCurve(edge, border, hi, false)
	nn := normalizeCurve(ncc, border, hi, true)
	nd := normalizeCurve(dark, border, hi, false)
	ns := normalizeCurve(sad, border, hi, true)
	fused := make([]float64, count)
	for i := range fused {
		fused[i] = ne[i] + nn[i] + 0.35*nd[i] + 0.35*ns[i]
	}
	out := []int{}
	edgeBest, nccBest := -1, -1
	for i := border; i <= hi; i++ {
		if edgeBest < 0 || ne[i] > ne[edgeBest] {
			edgeBest = i
		}
		if nccBest < 0 || nn[i] > nn[nccBest] {
			nccBest = i
		}
	}
	if edgeBest >= 0 && nccBest >= 0 && abs(edgeBest-nccBest) <= 4 {
		out = append(out, (edgeBest+nccBest+1)/2)
	}
	order := make([]int, count)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return fused[order[i]] > fused[order[j]] })
	for _, i := range order {
		if fused[i] <= localFusedFloor {
			break
		}
		far := true
		for _, existing := range out {
			if abs(existing-i) < separation {
				far = false
				break
			}
		}
		if far {
			out = append(out, i)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func normalizeCurve(values []float64, border, hi int, lowerIsBetter bool) []float64 {
	out := make([]float64, len(values))
	lo, top := math.Inf(1), math.Inf(-1)
	for i := border; i <= hi; i++ {
		lo, top = math.Min(lo, values[i]), math.Max(top, values[i])
	}
	if top-lo < 1e-9 {
		for i := range out {
			out[i] = -localLargeScore
		}
		return out
	}
	for i := range out {
		if i < border || i > hi {
			out[i] = -localLargeScore
		} else if lowerIsBetter {
			out[i] = (top - values[i]) / (top - lo)
		} else {
			out[i] = (values[i] - lo) / (top - lo)
		}
	}
	return out
}

func (m *localModel) edgeScore(offset int) float64 {
	if len(m.boundary) == 0 {
		return 0
	}
	sum := 0.0
	for _, p := range m.boundary {
		x := offset + p.X
		if x < 0 || x >= len(m.lum[p.Y]) {
			continue
		}
		gx, gy := m.gradX[p.Y][x], m.gradY[p.Y][x]
		sum += math.Sqrt(gx*gx + gy*gy)
	}
	return sum / float64(len(m.boundary))
}

func (m *localModel) nccScore(offset int) float64 {
	var points []localPoint
	for y := m.top; y <= m.bottom; y += 2 {
		for x := m.left; x <= m.right; x += 2 {
			if m.mask[y][x] {
				points = append(points, localPoint{x, y})
			}
		}
	}
	if len(points) == 0 {
		return localLargeScore
	}
	var pr, pg, pb, sr, sg, sb float64
	for _, p := range points {
		x := offset + p.X
		if x < 0 || x >= len(m.lum[p.Y]) {
			return localLargeScore
		}
		ar, ag, ab, _ := m.piece.At(p.X, p.Y).RGBA()
		br, bg, bb, _ := m.shade.At(x, p.Y).RGBA()
		pr, pg, pb = pr+float64(ar>>8), pg+float64(ag>>8), pb+float64(ab>>8)
		sr, sg, sb = sr+float64(br>>8), sg+float64(bg>>8), sb+float64(bb>>8)
	}
	n := float64(len(points))
	pr, pg, pb, sr, sg, sb = pr/n, pg/n, pb/n, sr/n, sg/n, sb/n
	num, da, db := 0.0, 0.0, 0.0
	for _, p := range points {
		x := offset + p.X
		ar, ag, ab, _ := m.piece.At(p.X, p.Y).RGBA()
		br, bg, bb, _ := m.shade.At(x, p.Y).RGBA()
		ard, agd, abd := float64(ar>>8)-pr, float64(ag>>8)-pg, float64(ab>>8)-pb
		brd, bgd, bbd := float64(br>>8)-sr, float64(bg>>8)-sg, float64(bb>>8)-sb
		num += ard*brd + agd*bgd + abd*bbd
		da += ard*ard + agd*agd + abd*abd
		db += brd*brd + bgd*bgd + bbd*bbd
	}
	if da <= 0 || db <= 0 {
		return -1
	}
	return -num / math.Sqrt(da*db)
}

func (m *localModel) darkScore(offset int) float64 {
	const ring = 8
	inside, insideCount, around, aroundCount := 0.0, 0, 0.0, 0
	y0, y1 := max(0, m.top-ring), min(len(m.mask)-1, m.bottom+ring)
	x0, x1 := max(0, m.left-ring), min(len(m.mask[0])-1, m.right+ring)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			sx := offset + x
			if sx < 0 || sx >= len(m.lum[y]) {
				continue
			}
			if m.mask[y][x] {
				inside += m.lum[y][sx]
				insideCount++
			} else {
				around += m.lum[y][sx]
				aroundCount++
			}
		}
	}
	if insideCount == 0 || aroundCount == 0 {
		return localLargeScore
	}
	return around/float64(aroundCount) - inside/float64(insideCount)
}

func (m *localModel) sadScore(offset int) float64 {
	sum, count := 0.0, 0
	for y := m.top; y <= m.bottom; y++ {
		for x := m.left; x <= m.right; x++ {
			if !m.mask[y][x] {
				continue
			}
			sx := offset + x
			if sx < 0 || sx >= len(m.lum[y]) {
				return localLargeScore
			}
			ar, ag, ab, _ := m.piece.At(x, y).RGBA()
			br, bg, bb, _ := m.shade.At(sx, y).RGBA()
			sum += math.Abs(float64(ar>>8)-float64(br>>8)) + math.Abs(float64(ag>>8)-float64(bg>>8)) + math.Abs(float64(ab>>8)-float64(bb>>8))
			count++
		}
	}
	if count == 0 {
		return localLargeScore
	}
	return sum / float64(count)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
