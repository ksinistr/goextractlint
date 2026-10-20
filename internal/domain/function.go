package domain

type Function struct {
	File             string        `json:"file"`
	Package          string        `json:"package"`
	Name             string        `json:"name"`
	Receiver         string        `json:"receiver,omitempty"`
	StartLine        int           `json:"start_line"`
	EndLine          int           `json:"end_line"`
	Source           string        `json:"source"`
	Context          []ContextItem `json:"context,omitempty"`
	ContextTruncated bool          `json:"context_truncated"`
}

type ContextItem struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

func (f Function) Identity() string {
	if f.Receiver != "" {
		return "(" + f.Receiver + ")." + f.Name
	}

	return f.Name
}
