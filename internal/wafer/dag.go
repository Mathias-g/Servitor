package wafer

import (
	"strconv"
	"strings"
)

// A node's effective identifier. Named nodes use their name; unnamed nodes are
// identified by their position in the list, which is what dependency references
// must resolve against.
func nodeID(w *Wafer, i int) string {
	if w.Nodes[i].Name != "" {
		return w.Nodes[i].Name
	}
	return strconv.Itoa(i)
}

// DAGNode is one node in the resolved dependency graph, in run order.
type DAGNode struct {
	// Name is the node's effective identifier (its `name` or list position).
	Name string
	// Type is the node type.
	Type string
	// DependsOn are the node identifiers this node depends on.
	DependsOn []string
	// Index is the node's position in the original `nodes:` list.
	Index int
}

// DAG is the workflow's resolved dependency graph in topological (run) order.
type DAG struct {
	Nodes []DAGNode
}

// ResolveDAG builds the dependency graph from a parsed Wafer, returning the
// nodes in run order and any issues (unknown node references and circular
// dependencies). It does not execute or contact anything; it is the shape of
// what the runner would do.
func ResolveDAG(w *Wafer) (DAG, []Issue) {
	var issues []Issue

	idByIndex := make([]string, len(w.Nodes))
	indexByName := map[string]int{}
	for i := range w.Nodes {
		id := nodeID(w, i)
		idByIndex[i] = id
		// A duplicate name is ambiguous; report it and skip (the first wins).
		if _, dup := indexByName[id]; dup {
			issues = append(issues, Issue{
				Path:    "/nodes/" + strconv.Itoa(i) + "/name",
				Code:    "duplicate_node_name",
				Message: "node name is used by more than one node",
			})
			continue
		}
		if w.Nodes[i].Name != "" {
			indexByName[id] = i
		}
	}

	deps := make([][]string, len(w.Nodes))
	indegree := make([]int, len(w.Nodes))
	adj := make([][]int, len(w.Nodes))

	for i := range w.Nodes {
		seen := map[string]bool{}
		for _, dep := range w.Nodes[i].DependsOn {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			depIdx, ok := indexByName[dep]
			if !ok {
				issues = append(issues, Issue{
					Path:       "/nodes/" + strconv.Itoa(i) + "/depends_on",
					Code:       "unknown_node_reference",
					Message:    "node depends on unknown node " + dep,
					Suggestion: dep,
				})
				continue
			}
			deps[i] = append(deps[i], dep)
			adj[depIdx] = append(adj[depIdx], i)
			indegree[i]++
		}
	}

	// Kahn's algorithm: repeatedly take a zero-indegree node, so the result is
	// a valid run order. Any leftover nodes form a cycle.
	var order []int
	queue := []int{}
	for i := range indegree {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range adj[n] {
			indegree[m]--
			if indegree[m] == 0 {
				queue = append(queue, m)
			}
		}
	}

	if len(order) != len(w.Nodes) {
		// A cycle exists among the remaining nodes.
		cycleNames := []string{}
		for i := range indegree {
			if indegree[i] > 0 {
				cycleNames = append(cycleNames, idByIndex[i])
			}
		}
		issues = append(issues, Issue{
			Path:    "/nodes",
			Code:    "circular_dependency",
			Message: "circular dependency among nodes: " + strings.Join(cycleNames, ", "),
		})
		return DAG{}, issues
	}

	dag := DAG{}
	for _, i := range order {
		dag.Nodes = append(dag.Nodes, DAGNode{
			Name:      idByIndex[i],
			Type:      w.Nodes[i].Type,
			DependsOn: deps[i],
			Index:     i,
		})
	}
	return dag, issues
}
