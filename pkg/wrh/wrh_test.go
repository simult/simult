package wrh

import (
	"sort"
	"testing"
)

func TestResponsibleNodes(t *testing.T) {
	nodeCount := 10
	respNodeCount := 3
	key := "test"

	nodes := make(Nodes, nodeCount)
	for i := range nodes {
		node := &nodes[i]
		node.Seed = uint32(i + 1)
		node.Weight = 1.0
		node.score = node.Score([]byte(key))
	}

	respNodes := ResponsibleNodes2(nodes, []byte(key), respNodeCount)

	sort.Sort(nodes)
	sort.Sort(respNodes)

	for i := range respNodes {
		if nodes[i].Seed != respNodes[i].Seed {
			t.Errorf("key=%v", key)
			t.Errorf("nodes=%v", nodes)
			t.Errorf("respNodes=%v", respNodes)
			t.FailNow()
		}
	}

	t.Logf("key=%v", key)
	t.Logf("nodes=%v", nodes)
	t.Logf("respNodes=%v", respNodes)
}
