package wafer

import (
	"strconv"
	"strings"
)

// A step's effective identifier. Named steps use their name; unnamed steps are
// identified by their position in the list, which is what dependency references
// must resolve against.
func stepID(w *Wafer, i int) string {
	if w.Steps[i].Name != "" {
		return w.Steps[i].Name
	}
	return strconv.Itoa(i)
}

// DAGStep is one node in the resolved dependency graph, in run order.
type DAGStep struct {
	// Name is the step's effective identifier (its `name` or list position).
	Name string
	// Type is the step type.
	Type string
	// DependsOn are the step identifiers this step depends on.
	DependsOn []string
	// Index is the step's position in the original `steps:` list.
	Index int
}

// DAG is the workflow's resolved dependency graph in topological (run) order.
type DAG struct {
	Steps []DAGStep
}

// ResolveDAG builds the dependency graph from a parsed Wafer, returning the
// steps in run order and any issues (unknown step references and circular
// dependencies). It does not execute or contact anything; it is the shape of
// what the runner would do.
func ResolveDAG(w *Wafer) (DAG, []Issue) {
	var issues []Issue

	idByIndex := make([]string, len(w.Steps))
	indexByName := map[string]int{}
	for i := range w.Steps {
		id := stepID(w, i)
		idByIndex[i] = id
		// A duplicate name is ambiguous; report it and skip (the first wins).
		if _, dup := indexByName[id]; dup {
			issues = append(issues, Issue{
				Path:    "/steps/" + strconv.Itoa(i) + "/name",
				Code:    "duplicate_step_name",
				Message: "step name is used by more than one step",
			})
			continue
		}
		if w.Steps[i].Name != "" {
			indexByName[id] = i
		}
	}

	deps := make([][]string, len(w.Steps))
	indegree := make([]int, len(w.Steps))
	adj := make([][]int, len(w.Steps))

	for i := range w.Steps {
		seen := map[string]bool{}
		for _, dep := range w.Steps[i].DependsOn {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			depIdx, ok := indexByName[dep]
			if !ok {
				issues = append(issues, Issue{
					Path:       "/steps/" + strconv.Itoa(i) + "/depends_on",
					Code:       "unknown_step_reference",
					Message:    "step depends on unknown step " + dep,
					Suggestion: dep,
				})
				continue
			}
			deps[i] = append(deps[i], dep)
			adj[depIdx] = append(adj[depIdx], i)
			indegree[i]++
		}
	}

	// Kahn's algorithm: repeatedly take a zero-indegree step, so the result is
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

	if len(order) != len(w.Steps) {
		// A cycle exists among the remaining steps.
		cycleNames := []string{}
		for i := range indegree {
			if indegree[i] > 0 {
				cycleNames = append(cycleNames, idByIndex[i])
			}
		}
		issues = append(issues, Issue{
			Path:    "/steps",
			Code:    "circular_dependency",
			Message: "circular dependency among steps: " + strings.Join(cycleNames, ", "),
		})
		return DAG{}, issues
	}

	dag := DAG{}
	for _, i := range order {
		dag.Steps = append(dag.Steps, DAGStep{
			Name:      idByIndex[i],
			Type:      w.Steps[i].Type,
			DependsOn: deps[i],
			Index:     i,
		})
	}
	return dag, issues
}
