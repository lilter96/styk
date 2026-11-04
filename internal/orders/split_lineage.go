package orders

import "slices"

// These are local proof-work bounds, not provider limits or a promise that any
// API accepts recursively splitting a particular posting/status.
const (
	MaxSplitLineageSteps = 64
	MaxSplitLineageHeads = 128
	MaxSplitLineageDepth = 16
)

type SplitLineageStep struct {
	Work   Work    `json:"work"`
	Before Posting `json:"before"`
}

type SplitLineageFacts struct {
	Scope           Scope              `json:"scope"`
	RootPosting     string             `json:"root_posting"`
	SelectedPosting string             `json:"selected_posting"`
	Splits          []SplitLineageStep `json:"splits"`
	CurrentHeads    []Posting          `json:"current_heads"`
}

// SplitLineageProof is a transient structural read proof. It grants NO provider
// command, reserve release, stock obligation retirement or native ownership.
// Reconstruct it from scoped durable work and fresh heads at each boundary;
// independent native zero/readback/baseline checks remain mandatory.
// In particular, Frontier must never be used to hide an unresolved ancestor.
type SplitLineageProof struct {
	facts    *SplitLineageFacts
	frontier []Posting
	path     []string
	selected Posting
}

func NewSplitLineage(facts SplitLineageFacts) (SplitLineageProof, error) {
	_, err := buildSplitLineage(facts)
	if err != nil {
		return SplitLineageProof{}, err
	}
	frozen, err := freezeSplitLineageFacts(facts)
	if err != nil {
		return SplitLineageProof{}, err
	}
	graph, err := buildSplitLineage(frozen)
	if err != nil {
		return SplitLineageProof{}, err
	}
	return SplitLineageProof{facts: &frozen, frontier: graph.frontier, path: graph.path, selected: graph.heads[facts.SelectedPosting]}, nil
}

func (proof SplitLineageProof) Validate() error {
	if proof.facts == nil {
		return ErrBlocked
	}
	_, err := buildSplitLineage(*proof.facts)
	return err
}

// Frontier returns the terminal generation heads, including any cancellation or
// delivery status as observed. It is not an independently settled stock pool.
func (proof SplitLineageProof) Frontier() []Posting {
	if proof.Validate() != nil {
		return nil
	}
	return cloneSplitLineagePostings(proof.frontier)
}

// PathWorkIDs returns actual split operations on the selected generation path.
func (proof SplitLineageProof) PathWorkIDs() []string {
	if proof.Validate() != nil {
		return nil
	}
	return slices.Clone(proof.path)
}

func (proof SplitLineageProof) CurrentMatches(posting Posting) bool {
	return proof.Validate() == nil && posting.PostingNumber == proof.facts.SelectedPosting && lineagePostingTrustworthy(posting) && splitTransferPostingEqual(proof.selected, posting)
}
