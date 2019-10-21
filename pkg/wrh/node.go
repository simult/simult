package wrh

import (
	"math"

	"github.com/spaolacci/murmur3"
)

type Node struct {
	Seed   uint32
	Weight float64
	Data   interface{}
	score  float64
}

func (nd *Node) Score(key []byte) float64 {
	_, h2 := murmur3.Sum128WithSeed(key, nd.Seed)
	hf := uint64ToFloat64(h2)
	x := 1.0 / (-math.Log(hf))
	return nd.Weight * x
}

type Nodes []Node

func (n Nodes) Len() int {
	return len(n)
}

func (n Nodes) Less(i, j int) bool {
	return n[i].score >= n[j].score
}

func (n Nodes) Swap(i, j int) {
	n[i], n[j] = n[j], n[i]
}
