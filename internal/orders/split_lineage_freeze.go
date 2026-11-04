package orders

import "encoding/json"

func freezeSplitLineageFacts(facts SplitLineageFacts) (SplitLineageFacts, error) {
	safe := facts
	safe.Splits = make([]SplitLineageStep, len(facts.Splits))
	for i, step := range facts.Splits {
		safe.Splits[i] = SplitLineageStep{Work: minimalSupersededWork(step.Work), Before: minimalSupersededPosting(step.Before)}
	}
	safe.CurrentHeads = minimalSupersededPostings(facts.CurrentHeads)
	raw, err := json.Marshal(safe)
	if err != nil {
		return SplitLineageFacts{}, ErrInvalid
	}
	var result SplitLineageFacts
	if json.Unmarshal(raw, &result) != nil {
		return SplitLineageFacts{}, ErrInvalid
	}
	return result, nil
}

func cloneSplitLineagePostings(postings []Posting) []Posting {
	raw, err := json.Marshal(minimalSupersededPostings(postings))
	if err != nil {
		return nil
	}
	var result []Posting
	if json.Unmarshal(raw, &result) != nil {
		return nil
	}
	return result
}
