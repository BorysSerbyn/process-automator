package agent

import "slices"

// BuiltinTool is a name builtinTools takes on an agent CLI backend.
type BuiltinTool struct {
	Name string
	// Allows says what allowing it lets a run do.
	Allows string
	// ReadOnly tools can't change anything: dry runs keep only these (built-in
	// tools run inside the CLI, where the gateway can't intercept them).
	ReadOnly bool
}

// BuiltinSet is an agent CLI's built-in tool vocabulary: the names a
// routine's builtinTools take on its backend.
type BuiltinSet struct {
	// Tools are the names it takes, with what each allows. When Open, any of
	// the CLI's own tool names goes, and Tools are the common ones.
	Tools []BuiltinTool
	Open  bool
}

// Names are the set's tool names, in order.
func (s BuiltinSet) Names() []string {
	out := make([]string, len(s.Tools))
	for i, t := range s.Tools {
		out[i] = t.Name
	}
	return out
}

func (s BuiltinSet) known(name string) bool { return slices.Contains(s.Names(), name) }

func (s BuiltinSet) readOnly(name string) bool {
	for _, t := range s.Tools {
		if t.Name == name {
			return t.ReadOnly
		}
	}
	return false
}
