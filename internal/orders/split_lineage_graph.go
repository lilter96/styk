package orders

import "slices"

type lineageOutput struct{ step, index int }
type splitLineageGraph struct {
	facts    SplitLineageFacts
	heads    map[string]Posting
	outputs  map[string][]lineageOutput
	parents  []int
	consumed map[lineageOutput]int
	root     int
	frontier []Posting
	path     []string
}

func buildSplitLineage(facts SplitLineageFacts) (splitLineageGraph, error) {
	graph := splitLineageGraph{facts: facts, heads: make(map[string]Posting, len(facts.CurrentHeads)), outputs: make(map[string][]lineageOutput), parents: make([]int, len(facts.Splits)), consumed: make(map[lineageOutput]int), root: -1}
	if facts.Scope.Validate() != nil || !validID(facts.RootPosting) || !validID(facts.SelectedPosting) || len(facts.Splits) == 0 || len(facts.Splits) > MaxSplitLineageSteps || len(facts.CurrentHeads) == 0 || len(facts.CurrentHeads) > MaxSplitLineageHeads {
		return graph, ErrBlocked
	}
	if err := graph.index(); err != nil {
		return graph, err
	}
	if err := graph.link(); err != nil {
		return graph, err
	}
	if err := graph.current(); err != nil {
		return graph, err
	}
	path, err := graph.ancestry(graph.selectedStep())
	if err != nil {
		return graph, err
	}
	graph.path = make([]string, len(path))
	for i, index := range path {
		graph.path[i] = facts.Splits[index].Work.ID
	}
	return graph, nil
}

func (graph *splitLineageGraph) index() error {
	seenWork, seenCommand := make(map[string]bool, len(graph.facts.Splits)), make(map[string]bool, len(graph.facts.Splits))
	for index, step := range graph.facts.Splits {
		if !lineageStepValid(graph.facts.Scope, step) || seenWork[step.Work.ID] || seenCommand[step.Work.Command.ID] {
			return ErrBlocked
		}
		seenWork[step.Work.ID], seenCommand[step.Work.Command.ID] = true, true
		graph.parents[index] = -1
		for output, posting := range step.Work.Receipt.ObservedPostings {
			graph.outputs[posting.PostingNumber] = append(graph.outputs[posting.PostingNumber], lineageOutput{index, output})
		}
	}
	for _, head := range graph.facts.CurrentHeads {
		if !lineagePostingTrustworthy(head) || head.Scope != graph.facts.Scope {
			return ErrBlocked
		}
		if _, duplicate := graph.heads[head.PostingNumber]; duplicate {
			return ErrConflict
		}
		graph.heads[head.PostingNumber] = head
	}
	return nil
}

func (graph *splitLineageGraph) link() error {
	for index, step := range graph.facts.Splits {
		var candidates []lineageOutput
		for _, output := range graph.outputs[step.Before.PostingNumber] {
			if output.step != index && splitTransferSameAllocation(graph.output(output), step.Before) {
				candidates = append(candidates, output)
			}
		}
		if len(candidates) > 1 {
			return ErrConflict
		}
		if len(candidates) == 0 {
			if graph.root >= 0 || step.Before.PostingNumber != graph.facts.RootPosting || step.Before.ParentPostingNumber.IsKnown() {
				return ErrBlocked
			}
			graph.root = index
			continue
		}
		previous := candidates[0]
		if _, duplicate := graph.consumed[previous]; duplicate {
			return ErrConflict
		}
		graph.parents[index] = previous.step
		graph.consumed[previous] = index
	}
	if graph.root < 0 {
		return ErrBlocked
	}
	for index, step := range graph.facts.Splits {
		if _, err := graph.ancestry(index); err != nil || !graph.parentConsistent(index, step.Before) {
			return ErrBlocked
		}
	}
	return graph.uniqueBirths()
}

func (graph splitLineageGraph) ancestry(index int) ([]int, error) {
	seen := make(map[int]bool, MaxSplitLineageDepth)
	path := make([]int, 0, MaxSplitLineageDepth)
	for index >= 0 {
		if seen[index] || len(path) >= MaxSplitLineageDepth {
			return nil, ErrBlocked
		}
		seen[index] = true
		path = append(path, index)
		index = graph.parents[index]
	}
	if len(path) == 0 || path[len(path)-1] != graph.root {
		return nil, ErrBlocked
	}
	slices.Reverse(path)
	return path, nil
}

func (graph splitLineageGraph) uniqueBirths() error {
	for number, outputs := range graph.outputs {
		births := 0
		if number == graph.facts.RootPosting {
			births = 1
		}
		for _, output := range outputs {
			if graph.facts.Splits[output.step].Before.PostingNumber != number {
				births++
			}
		}
		if births > 1 {
			return ErrConflict
		}
	}
	return nil
}

func (graph splitLineageGraph) parentConsistent(index int, posting Posting) bool {
	if !posting.ParentPostingNumber.IsKnown() {
		return true
	}
	parent := *posting.ParentPostingNumber.Value
	path, err := graph.ancestry(index)
	if err != nil {
		return false
	}
	for _, ancestor := range path {
		if graph.facts.Splits[ancestor].Before.PostingNumber == parent && parent != posting.PostingNumber {
			return true
		}
	}
	return false
}

func (graph splitLineageGraph) output(key lineageOutput) Posting {
	return graph.facts.Splits[key.step].Work.Receipt.ObservedPostings[key.index]
}
