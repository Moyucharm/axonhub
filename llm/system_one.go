package llm

// SystemOneRequest carries the original TypeSafe System One JSON body.
// The protocol is intentionally opaque so new question and state fields can be
// proxied without changing the unified model.
type SystemOneRequest struct {
	Body []byte `json:"-"`
}

// SystemOneResponse carries the upstream TypeSafe System One JSON response verbatim.
type SystemOneResponse struct {
	Body []byte `json:"-"`
}
