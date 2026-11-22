package ui

import (
	"strings"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/cli"
)

// entry is a setting in its group, labelled without its groups' names.
type entry struct {
	label   string
	setting cli.Setting
}

// group is settings grouped by their dotted keys, two levels deep:
// llm.budget.window sits in llm › budget as "window", llm.backend in llm as
// "backend". Groups and entries keep the order the core lists them in; in a
// group, its own entries come before its subgroups (automator-ui's KeyTree).
type group struct {
	// id is the group's path ("llm", "llm.budget").
	id, name string
	entries  []entry
	groups   []*group
}

func keyTree(settings []cli.Setting) []*group {
	var tree []*group
	find := func(list []*group, name string) *group {
		for _, g := range list {
			if g.name == name {
				return g
			}
		}
		return nil
	}
	for _, s := range settings {
		parts := strings.Split(s.Key, ".")
		top := find(tree, parts[0])
		if top == nil {
			top = &group{id: parts[0], name: parts[0]}
			tree = append(tree, top)
		}
		switch len(parts) {
		case 1:
			top.entries = append(top.entries, entry{parts[0], s})
		case 2:
			top.entries = append(top.entries, entry{parts[1], s})
		default:
			sub := find(top.groups, parts[1])
			if sub == nil {
				sub = &group{id: parts[0] + "." + parts[1], name: parts[1]}
				top.groups = append(top.groups, sub)
			}
			sub.entries = append(sub.entries, entry{strings.Join(parts[2:], "."), s})
		}
	}
	return tree
}

// count is how many settings a group holds, subgroups included.
func (g *group) count() int {
	n := len(g.entries)
	for _, sub := range g.groups {
		n += sub.count()
	}
	return n
}
