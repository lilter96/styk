package orders

import (
	"slices"
	"strings"
)

func (graph *splitLineageGraph) current() error {
	involved := make(map[string]bool, len(graph.heads))
	for _, step := range graph.facts.Splits {
		involved[step.Before.PostingNumber] = true
	}
	for number := range graph.outputs {
		involved[number] = true
	}
	if len(involved) != len(graph.heads) {
		return ErrBlocked
	}
	for number := range involved {
		head, found := graph.heads[number]
		if !found {
			return ErrBlocked
		}
		leaf, found, err := graph.terminalOutput(number)
		if err != nil {
			return err
		}
		if found {
			if knownLineageRetired(head) || !splitTransferSameAllocation(graph.output(leaf), head) || !graph.parentConsistent(leaf.step, head) {
				return ErrBlocked
			}
			graph.frontier = append(graph.frontier, head)
			continue
		}
		retired := graph.lastSourceStep(number)
		if retired < 0 || !knownLineageRetired(head) || !splitTransferSameAllocation(graph.facts.Splits[retired].Before, head) || !graph.parentConsistent(retired, head) {
			return ErrBlocked
		}
	}
	if _, found := graph.heads[graph.facts.SelectedPosting]; !found {
		return ErrBlocked
	}
	slices.SortFunc(graph.frontier, func(a, b Posting) int { return strings.Compare(a.PostingNumber, b.PostingNumber) })
	return nil
}

func (graph splitLineageGraph) terminalOutput(number string) (lineageOutput, bool, error) {
	var result lineageOutput
	found := false
	for _, output := range graph.outputs[number] {
		if _, consumed := graph.consumed[output]; consumed {
			continue
		}
		if found {
			return result, false, ErrConflict
		}
		result, found = output, true
	}
	return result, found, nil
}

func (graph splitLineageGraph) lastSourceStep(number string) int {
	result := -1
	for index, step := range graph.facts.Splits {
		if step.Before.PostingNumber != number {
			continue
		}
		if result < 0 {
			result = index
			continue
		}
		path, _ := graph.ancestry(index)
		if slices.Contains(path, result) {
			result = index
		}
	}
	return result
}

func (graph splitLineageGraph) selectedStep() int {
	output, found, _ := graph.terminalOutput(graph.facts.SelectedPosting)
	if found {
		return output.step
	}
	return graph.lastSourceStep(graph.facts.SelectedPosting)
}

func knownLineageRetired(posting Posting) bool {
	return posting.Status.IsKnown() && *posting.Status.Value == SupersededSplitParentStatus
}
