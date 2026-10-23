package resolve

// Entry is the outcome of resolving one envfile.Entry.
type Entry struct {
	Key   string
	Value string // empty when Err != nil
	Err   error  // typically *provider.Error, nil on success
	Line  int
}

// Result holds every entry's outcome, in the same order as the input.
type Result struct {
	Entries []Entry
}

// OK returns only successfully resolved entries, preserving order.
func (r *Result) OK() []Entry {
	out := make([]Entry, 0, len(r.Entries))
	for _, e := range r.Entries {
		if e.Err == nil {
			out = append(out, e)
		}
	}
	return out
}

// Failed returns only failed entries, preserving order.
func (r *Result) Failed() []Entry {
	out := make([]Entry, 0)
	for _, e := range r.Entries {
		if e.Err != nil {
			out = append(out, e)
		}
	}
	return out
}

// HasErrors reports whether any entry failed to resolve.
func (r *Result) HasErrors() bool {
	for _, e := range r.Entries {
		if e.Err != nil {
			return true
		}
	}
	return false
}
