package wrh

func uint64ToFloat64(v uint64) float64 {
	ones := uint64(^uint64(0) >> (64 - 53))
	zeros := float64(1 << 53)
	return float64(v&ones) / zeros
}

func ResponsibleNodes(nodes Nodes, key []byte, respNodes Nodes) {
	respNodesLen := len(respNodes)
	if respNodesLen <= 0 {
		return
	}
	nodesLen := len(nodes)
	for i := range respNodes {
		respNodes[i] = Node{}
	}
	for i := 0; i < nodesLen; i++ {
		sc := nodes[i].Score(key)
		k := -1
		for j := 0; j < respNodesLen; j++ {
			if sc > respNodes[j].score {
				if k < 0 || (respNodes[k].score > respNodes[j].score) {
					k = j
				}
			}
		}
		if k >= 0 {
			respNodes[k] = nodes[i]
			respNodes[k].score = sc
		}
	}
}

func ResponsibleNodes2(nodes Nodes, key []byte, count int) Nodes {
	if count <= 0 {
		return nil
	}
	respNodes := make(Nodes, count)
	ResponsibleNodes(nodes, key, respNodes)
	return respNodes
}

func FindSeed(nodes Nodes, seed uint32) int {
	for i, j := 0, len(nodes); i < j; i++ {
		if nodes[i].Seed == seed {
			return i
		}
	}
	return -1
}

func MaxScore(nodes Nodes) uint32 {
	var result uint32
	var max float64
	for i, j := 0, len(nodes); i < j; i++ {
		if nodes[i].score > max {
			result = nodes[i].Seed
			max = nodes[i].score
		}
	}
	return result
}

func MergeNodes(nodes1, nodes2 Nodes, mergedNodesIn Nodes) (mergedNodes Nodes) {
	mergedNodes = mergedNodesIn
	for i := range nodes1 {
		if FindSeed(mergedNodes, nodes1[i].Seed) >= 0 {
			continue
		}
		mergedNodes = append(mergedNodes, nodes1[i])
	}
	for i := range nodes2 {
		if FindSeed(mergedNodes, nodes2[i].Seed) >= 0 {
			continue
		}
		mergedNodes = append(mergedNodes, nodes2[i])
	}
	return
}
